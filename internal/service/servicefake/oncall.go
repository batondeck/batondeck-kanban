package servicefake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

type oncallFake struct{ f *Fake }

func (x oncallFake) CreateIntegration(ctx context.Context, caller domain.Caller, req service.CreateIntegrationReq) (service.CreateIntegrationResp, error) {
	return call[service.CreateIntegrationResp](x.f, "Oncall.CreateIntegration", caller, req)
}

func (x oncallFake) UpdateIntegration(ctx context.Context, caller domain.Caller, req service.UpdateIntegrationReq) (service.UpdateIntegrationResp, error) {
	return call[service.UpdateIntegrationResp](x.f, "Oncall.UpdateIntegration", caller, req)
}

func (x oncallFake) DeleteIntegration(ctx context.Context, caller domain.Caller, req service.DeleteIntegrationReq) (service.DeleteIntegrationResp, error) {
	return call[service.DeleteIntegrationResp](x.f, "Oncall.DeleteIntegration", caller, req)
}

func (x oncallFake) ListIntegrations(ctx context.Context, caller domain.Caller, req service.ListIntegrationsReq) (service.ListIntegrationsResp, error) {
	return call[service.ListIntegrationsResp](x.f, "Oncall.ListIntegrations", caller, req)
}

func (x oncallFake) RotateIntegrationSecret(ctx context.Context, caller domain.Caller, req service.RotateIntegrationSecretReq) (service.RotateIntegrationSecretResp, error) {
	return call[service.RotateIntegrationSecretResp](x.f, "Oncall.RotateIntegrationSecret", caller, req)
}

func (x oncallFake) ListDeliveries(ctx context.Context, caller domain.Caller, req service.ListDeliveriesReq) (service.ListDeliveriesResp, error) {
	return call[service.ListDeliveriesResp](x.f, "Oncall.ListDeliveries", caller, req)
}

func (x oncallFake) ReadDeliveryRaw(ctx context.Context, caller domain.Caller, req service.ReadDeliveryRawReq) (service.ReadDeliveryRawResp, error) {
	return call[service.ReadDeliveryRawResp](x.f, "Oncall.ReadDeliveryRaw", caller, req)
}

func (x oncallFake) ReplayDelivery(ctx context.Context, caller domain.Caller, req service.ReplayDeliveryReq) (service.ReplayDeliveryResp, error) {
	return call[service.ReplayDeliveryResp](x.f, "Oncall.ReplayDelivery", caller, req)
}

func (x oncallFake) SendTestEvent(ctx context.Context, caller domain.Caller, req service.SendTestEventReq) (service.SendTestEventResp, error) {
	return call[service.SendTestEventResp](x.f, "Oncall.SendTestEvent", caller, req)
}

var _ service.Oncall = oncallFake{}
