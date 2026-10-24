package servicefake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

type notificationsFake struct{ f *Fake }

func (x notificationsFake) List(ctx context.Context, caller domain.Caller, req service.ListNotificationsReq) (service.ListNotificationsResp, error) {
	return call[service.ListNotificationsResp](x.f, "Notifications.List", caller, req)
}

func (x notificationsFake) MarkRead(ctx context.Context, caller domain.Caller, req service.MarkNotificationsReadReq) (service.MarkNotificationsReadResp, error) {
	return call[service.MarkNotificationsReadResp](x.f, "Notifications.MarkRead", caller, req)
}

type profilesFake struct{ f *Fake }

func (x profilesFake) Register(ctx context.Context, caller domain.Caller, req service.RegisterAgentProfileReq) (service.RegisterAgentProfileResp, error) {
	return call[service.RegisterAgentProfileResp](x.f, "Profiles.Register", caller, req)
}

func (x profilesFake) SkillStats(ctx context.Context, caller domain.Caller, req service.GetSkillStatsReq) (service.GetSkillStatsResp, error) {
	return call[service.GetSkillStatsResp](x.f, "Profiles.SkillStats", caller, req)
}

type anomaliesFake struct{ f *Fake }

func (x anomaliesFake) Detect(ctx context.Context, caller domain.Caller, req service.DetectAnomaliesReq) (service.DetectAnomaliesResp, error) {
	return call[service.DetectAnomaliesResp](x.f, "Anomalies.Detect", caller, req)
}

type rankingFake struct{ f *Fake }

func (x rankingFake) Rank(ctx context.Context, caller domain.Caller, req service.RankTasksReq) (service.RankTasksResp, error) {
	return call[service.RankTasksResp](x.f, "Ranking.Rank", caller, req)
}

func (x rankingFake) Orchestrate(ctx context.Context, caller domain.Caller, req service.OrchestrateSubtasksReq) (service.OrchestrateSubtasksResp, error) {
	return call[service.OrchestrateSubtasksResp](x.f, "Ranking.Orchestrate", caller, req)
}

type grantsFake struct{ f *Fake }

func (x grantsFake) Grant(ctx context.Context, caller domain.Caller, req service.GrantCapabilitiesReq) (service.GrantCapabilitiesResp, error) {
	return call[service.GrantCapabilitiesResp](x.f, "Grants.Grant", caller, req)
}
