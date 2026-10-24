package servicefake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

type runsFake struct{ f *Fake }

func (x runsFake) Start(ctx context.Context, caller domain.Caller, req service.StartRunsReq) (service.StartRunsResp, error) {
	return call[service.StartRunsResp](x.f, "Runs.Start", caller, req)
}

func (x runsFake) Open(ctx context.Context, caller domain.Caller, req service.OpenRunReq) (service.OpenRunResp, error) {
	return call[service.OpenRunResp](x.f, "Runs.Open", caller, req)
}

func (x runsFake) Pick(ctx context.Context, caller domain.Caller, req service.PickRunReq) (service.PickRunResp, error) {
	return call[service.PickRunResp](x.f, "Runs.Pick", caller, req)
}

func (x runsFake) Cancel(ctx context.Context, caller domain.Caller, req service.CancelRunsReq) (service.CancelRunsResp, error) {
	return call[service.CancelRunsResp](x.f, "Runs.Cancel", caller, req)
}

func (x runsFake) List(ctx context.Context, caller domain.Caller, req service.ListRunsReq) (service.ListRunsResp, error) {
	return call[service.ListRunsResp](x.f, "Runs.List", caller, req)
}

type sprintsFake struct{ f *Fake }

func (x sprintsFake) Create(ctx context.Context, caller domain.Caller, req service.CreateSprintReq) (service.CreateSprintResp, error) {
	return call[service.CreateSprintResp](x.f, "Sprints.Create", caller, req)
}

func (x sprintsFake) Update(ctx context.Context, caller domain.Caller, req service.UpdateSprintReq) (service.UpdateSprintResp, error) {
	return call[service.UpdateSprintResp](x.f, "Sprints.Update", caller, req)
}

func (x sprintsFake) Set(ctx context.Context, caller domain.Caller, req service.SetSprintReq) (service.SetSprintResp, error) {
	return call[service.SetSprintResp](x.f, "Sprints.Set", caller, req)
}

func (x sprintsFake) Get(ctx context.Context, caller domain.Caller, req service.GetSprintReq) (service.GetSprintResp, error) {
	return call[service.GetSprintResp](x.f, "Sprints.Get", caller, req)
}

func (x sprintsFake) List(ctx context.Context, caller domain.Caller, req service.ListSprintsReq) (service.ListSprintsResp, error) {
	return call[service.ListSprintsResp](x.f, "Sprints.List", caller, req)
}
