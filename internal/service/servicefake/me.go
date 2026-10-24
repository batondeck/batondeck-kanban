package servicefake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func (x sessionsFake) DisconnectAgent(ctx context.Context, caller domain.Caller, req service.DisconnectAgentReq) (service.DisconnectAgentResp, error) {
	return call[service.DisconnectAgentResp](x.f, "Sessions.DisconnectAgent", caller, req)
}

type meFake struct{ f *Fake }

func (x meFake) GetMySettings(ctx context.Context, caller domain.Caller, req service.GetMySettingsReq) (service.MySettings, error) {
	return call[service.MySettings](x.f, "Me.GetMySettings", caller, req)
}

func (x meFake) SetMySettings(ctx context.Context, caller domain.Caller, req service.SetMySettingsReq) (service.MySettings, error) {
	return call[service.MySettings](x.f, "Me.SetMySettings", caller, req)
}

type auditFake struct{ f *Fake }

func (x auditFake) ListAuditLog(ctx context.Context, caller domain.Caller, req service.ListAuditLogReq) (service.ListAuditLogResp, error) {
	return call[service.ListAuditLogResp](x.f, "Audit.ListAuditLog", caller, req)
}

type analyticsFake struct{ f *Fake }

func (x analyticsFake) Operations(ctx context.Context, caller domain.Caller, req service.OperationsReq) (service.OperationsResp, error) {
	return call[service.OperationsResp](x.f, "Analytics.Operations", caller, req)
}

var (
	_ service.Me        = meFake{}
	_ service.Audit     = auditFake{}
	_ service.Analytics = analyticsFake{}
)
