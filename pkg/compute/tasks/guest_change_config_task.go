package tasks

import (
	"context"

	"yunion.io/x/jsonutils"

	"yunion.io/x/onecloud/pkg/cloudcommon/db"
	"yunion.io/x/onecloud/pkg/cloudcommon/db/lockman"
	"yunion.io/x/onecloud/pkg/cloudcommon/db/taskman"
	"yunion.io/x/onecloud/pkg/compute/models"
	"yunion.io/x/onecloud/pkg/util/logclient"
)

type GuestChangeConfigTask struct {
	SGuestBaseTask
}

func init() {
	taskman.RegisterTask(GuestChangeConfigTask{})
}

func (self *GuestChangeConfigTask) OnInit(ctx context.Context, obj db.IStandaloneModel, data jsonutils.JSONObject) {
	_, err := self.Params.Get("resize")
	if err == nil {
		self.SetStage("on_disks_resize_complete", nil)
		self.OnDisksResizeComplete(ctx, obj, data)
	} else {
		guest := obj.(*models.SGuest)
		self.DoCreateDisksTask(ctx, guest)
	}
}

func (self *GuestChangeConfigTask) OnDisksResizeComplete(ctx context.Context, obj db.IStandaloneModel, data jsonutils.JSONObject) {
	iResizeDisks, err := self.Params.Get("resize")
	if iResizeDisks == nil || err != nil {
		self.markStageFailed(obj, ctx, err.Error())
		return
	}
	resizeDisks := iResizeDisks.(*jsonutils.JSONArray)
	for i := 0; i < resizeDisks.Length(); i++ {
		iResizeSet, err := resizeDisks.GetAt(i)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		resizeSet := iResizeSet.(*jsonutils.JSONArray)
		diskId, err := resizeSet.GetAt(0)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		idStr, err := diskId.GetString()
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		jSize, err := resizeSet.GetAt(1)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		size, err := jSize.Int()
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		iDisk, err := models.DiskManager.FetchById(idStr)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		disk := iDisk.(*models.SDisk)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(disk, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		if disk.DiskSize < int(size) {
			var pendingUsage models.SQuota
			err = self.GetPendingUsage(&pendingUsage)
			if err != nil {
				self.markStageFailed(obj, ctx, err.Error())
				logclient.AddActionLog(disk, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
				return
			}
			disk.StartDiskResizeTask(ctx, self.UserCred, size, self.GetTaskId(), &pendingUsage)
			return
		}
	}
	guest := obj.(*models.SGuest)
	self.DoCreateDisksTask(ctx, guest)
}

func (self *GuestChangeConfigTask) DoCreateDisksTask(ctx context.Context, guest *models.SGuest) {
	iCreateData, err := self.Params.Get("create")
	if err != nil || iCreateData == nil {
		self.OnCreateDisksComplete(ctx, guest, nil)
		return
	}
	data := (iCreateData).(*jsonutils.JSONDict)
	self.SetStage("on_create_disks_complete", nil)
	guest.StartGuestCreateDiskTask(ctx, self.UserCred, data, self.GetTaskId())

}

func (self *GuestChangeConfigTask) OnCreateDisksCompleteFailed(ctx context.Context, obj db.IStandaloneModel, err jsonutils.JSONObject) {
	self.markStageFailed(obj, ctx, err.String())
	logclient.AddActionLog(obj, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
}

func (self *GuestChangeConfigTask) OnCreateDisksComplete(ctx context.Context, obj db.IStandaloneModel, data jsonutils.JSONObject) {
	iVcpuCount, errCpu := self.Params.Get("vcpu_count")
	iVmemSize, errMem := self.Params.Get("vmem_size")
	var vcpuCount, vmemSize int64
	var err error
	guest := obj.(*models.SGuest)
	if errCpu == nil || errMem == nil {
		if iVcpuCount != nil {
			vcpuCount, err = iVcpuCount.Int()
			if err != nil {
				self.markStageFailed(obj, ctx, err.Error())
				logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
				return
			}
		}
		if iVmemSize != nil {
			vmemSize, err = iVmemSize.Int()
			if err != nil {
				self.markStageFailed(obj, ctx, err.Error())
				logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
				return
			}
		}
		err = guest.GetDriver().RequestChangeVmConfig(ctx, guest, self, vcpuCount, vmemSize)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		var addCpu, addMem = 0, 0
		if vcpuCount > 0 {
			addCpu = int(vcpuCount - int64(guest.VcpuCount))
			if addCpu < 0 {
				addCpu = 0
			}
		}
		if vmemSize > 0 {
			addMem = int(vmemSize - int64(guest.VmemSize))
			if addMem < 0 {
				addMem = 0
			}
		}
		_, err = guest.GetModelManager().TableSpec().Update(guest, func() error {
			if vcpuCount > 0 {
				guest.VcpuCount = int8(vcpuCount)
			}
			if vmemSize > 0 {
				guest.VmemSize = int(vmemSize)
			}
			return nil
		})
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		var pendingUsage models.SQuota
		err = self.GetPendingUsage(&pendingUsage)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		// ownerCred := guest.GetOwnerUserCred()
		var cancelUsage models.SQuota
		if addCpu > 0 {
			cancelUsage.Cpu = addCpu
		}
		if addMem > 0 {
			cancelUsage.Memory = addMem
		}

		lockman.LockClass(ctx, guest.GetModelManager(), guest.ProjectId)
		defer lockman.ReleaseClass(ctx, guest.GetModelManager(), guest.ProjectId)

		err = models.QuotaManager.CancelPendingUsage(ctx, self.UserCred, guest.ProjectId, &pendingUsage, &cancelUsage)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
		err = self.SetPendingUsage(&pendingUsage)
		if err != nil {
			self.markStageFailed(obj, ctx, err.Error())
			logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
			return
		}
	}
	self.SetStage("on_sync_status_complete", nil)
	err = guest.StartSyncstatus(ctx, self.UserCred, self.GetTaskId())
	if err != nil {
		self.markStageFailed(obj, ctx, err.Error())
		logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, err, self.UserCred, false)
		return
	}
}

func (self *GuestChangeConfigTask) OnSyncStatusComplete(ctx context.Context, obj db.IStandaloneModel, data jsonutils.JSONObject) {
	guest := obj.(*models.SGuest)
	if guest.Status == models.VM_READY && jsonutils.QueryBoolean(self.Params, "auto_start", false) {
		self.SetStage("on_guest_start_complete", nil)
		guest.StartGueststartTask(ctx, self.UserCred, nil, self.GetTaskId())
		logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, "", self.UserCred, true)
	} else {
		dt := jsonutils.NewDict()
		dt.Add(jsonutils.NewString(guest.Id), "id")
		self.SetStageComplete(ctx, dt)
		logclient.AddActionLog(guest, logclient.ACT_VM_CHANGE_FLAVOR, "", self.UserCred, true)
	}
}

func (self *GuestChangeConfigTask) OnGuestStartComplete(ctx context.Context, obj db.IStandaloneModel, data jsonutils.JSONObject) {
	guest := obj.(*models.SGuest)
	dt := jsonutils.NewDict()
	dt.Add(jsonutils.NewString(guest.Id), "id")
	self.SetStageComplete(ctx, dt)
}

func (self *GuestChangeConfigTask) markStageFailed(obj db.IStandaloneModel, ctx context.Context, reason string) {
	guest := obj.(*models.SGuest)
	guest.SetStatus(self.UserCred, models.VM_CHANGE_FLAVOR_FAIL, reason)
	self.SetStageFailed(ctx, reason)
}