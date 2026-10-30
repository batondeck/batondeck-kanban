package servicefake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

type projectsFake struct{ f *Fake }

func (x projectsFake) Create(ctx context.Context, caller domain.Caller, req service.CreateProjectReq) (service.CreateProjectResp, error) {
	return call[service.CreateProjectResp](x.f, "Projects.Create", caller, req)
}

func (x projectsFake) Get(ctx context.Context, caller domain.Caller, req service.GetProjectReq) (service.GetProjectResp, error) {
	return call[service.GetProjectResp](x.f, "Projects.Get", caller, req)
}

func (x projectsFake) List(ctx context.Context, caller domain.Caller, req service.ListProjectsReq) (service.ListProjectsResp, error) {
	return call[service.ListProjectsResp](x.f, "Projects.List", caller, req)
}

func (x projectsFake) UpdateSettings(ctx context.Context, caller domain.Caller, req service.UpdateSettingsReq) (service.UpdateSettingsResp, error) {
	return call[service.UpdateSettingsResp](x.f, "Projects.UpdateSettings", caller, req)
}

func (x projectsFake) Delete(ctx context.Context, caller domain.Caller, req service.DeleteProjectReq) (service.DeleteProjectResp, error) {
	return call[service.DeleteProjectResp](x.f, "Projects.Delete", caller, req)
}

func (x projectsFake) AddMember(ctx context.Context, caller domain.Caller, req service.AddMemberReq) (service.AddMemberResp, error) {
	return call[service.AddMemberResp](x.f, "Projects.AddMember", caller, req)
}

func (x projectsFake) RemoveMember(ctx context.Context, caller domain.Caller, req service.RemoveMemberReq) (service.RemoveMemberResp, error) {
	return call[service.RemoveMemberResp](x.f, "Projects.RemoveMember", caller, req)
}

type boardsFake struct{ f *Fake }

func (x boardsFake) Create(ctx context.Context, caller domain.Caller, req service.CreateBoardReq) (service.CreateBoardResp, error) {
	return call[service.CreateBoardResp](x.f, "Boards.Create", caller, req)
}

func (x boardsFake) List(ctx context.Context, caller domain.Caller, req service.ListBoardsReq) (service.ListBoardsResp, error) {
	return call[service.ListBoardsResp](x.f, "Boards.List", caller, req)
}

func (x boardsFake) Get(ctx context.Context, caller domain.Caller, req service.GetBoardReq) (service.GetBoardResp, error) {
	return call[service.GetBoardResp](x.f, "Boards.Get", caller, req)
}

func (x boardsFake) AddColumn(ctx context.Context, caller domain.Caller, req service.AddColumnReq) (service.AddColumnResp, error) {
	return call[service.AddColumnResp](x.f, "Boards.AddColumn", caller, req)
}

func (x boardsFake) UpdateColumn(ctx context.Context, caller domain.Caller, req service.UpdateColumnReq) (service.UpdateColumnResp, error) {
	return call[service.UpdateColumnResp](x.f, "Boards.UpdateColumn", caller, req)
}

func (x boardsFake) RemoveColumn(ctx context.Context, caller domain.Caller, req service.RemoveColumnReq) (service.RemoveColumnResp, error) {
	return call[service.RemoveColumnResp](x.f, "Boards.RemoveColumn", caller, req)
}

type tasksFake struct{ f *Fake }

func (x tasksFake) Create(ctx context.Context, caller domain.Caller, req service.CreateTaskReq) (service.CreateTaskResp, error) {
	return call[service.CreateTaskResp](x.f, "Tasks.Create", caller, req)
}

func (x tasksFake) Get(ctx context.Context, caller domain.Caller, req service.GetTaskReq) (service.GetTaskResp, error) {
	return call[service.GetTaskResp](x.f, "Tasks.Get", caller, req)
}

func (x tasksFake) Update(ctx context.Context, caller domain.Caller, req service.UpdateTaskReq) (service.UpdateTaskResp, error) {
	return call[service.UpdateTaskResp](x.f, "Tasks.Update", caller, req)
}

func (x tasksFake) Move(ctx context.Context, caller domain.Caller, req service.MoveTaskReq) (service.MoveTaskResp, error) {
	return call[service.MoveTaskResp](x.f, "Tasks.Move", caller, req)
}

func (x tasksFake) List(ctx context.Context, caller domain.Caller, req service.ListTasksReq) (service.ListTasksResp, error) {
	return call[service.ListTasksResp](x.f, "Tasks.List", caller, req)
}

func (x tasksFake) Delete(ctx context.Context, caller domain.Caller, req service.DeleteTaskReq) (service.DeleteTaskResp, error) {
	return call[service.DeleteTaskResp](x.f, "Tasks.Delete", caller, req)
}

type lifecycleFake struct{ f *Fake }

func (x lifecycleFake) Claim(ctx context.Context, caller domain.Caller, req service.ClaimTaskReq) (service.ClaimTaskResp, error) {
	return call[service.ClaimTaskResp](x.f, "Lifecycle.Claim", caller, req)
}

func (x lifecycleFake) ClaimNext(ctx context.Context, caller domain.Caller, req service.ClaimNextReq) (service.ClaimNextResp, error) {
	return call[service.ClaimNextResp](x.f, "Lifecycle.ClaimNext", caller, req)
}

func (x lifecycleFake) Heartbeat(ctx context.Context, caller domain.Caller, req service.HeartbeatTaskReq) (service.HeartbeatTaskResp, error) {
	return call[service.HeartbeatTaskResp](x.f, "Lifecycle.Heartbeat", caller, req)
}

func (x lifecycleFake) Release(ctx context.Context, caller domain.Caller, req service.ReleaseTaskReq) (service.ReleaseTaskResp, error) {
	return call[service.ReleaseTaskResp](x.f, "Lifecycle.Release", caller, req)
}

func (x lifecycleFake) Next(ctx context.Context, caller domain.Caller, req service.NextTaskReq) (service.NextTaskResp, error) {
	return call[service.NextTaskResp](x.f, "Lifecycle.Next", caller, req)
}

func (x lifecycleFake) Complete(ctx context.Context, caller domain.Caller, req service.CompleteTaskReq) (service.CompleteTaskResp, error) {
	return call[service.CompleteTaskResp](x.f, "Lifecycle.Complete", caller, req)
}

func (x lifecycleFake) Block(ctx context.Context, caller domain.Caller, req service.BlockTaskReq) (service.BlockTaskResp, error) {
	return call[service.BlockTaskResp](x.f, "Lifecycle.Block", caller, req)
}

func (x lifecycleFake) Fail(ctx context.Context, caller domain.Caller, req service.FailTaskReq) (service.FailTaskResp, error) {
	return call[service.FailTaskResp](x.f, "Lifecycle.Fail", caller, req)
}

func (x lifecycleFake) Reap(ctx context.Context, caller domain.Caller, req service.ReapStaleLeasesReq) (service.ReapStaleLeasesResp, error) {
	return call[service.ReapStaleLeasesResp](x.f, "Lifecycle.Reap", caller, req)
}

func (x lifecycleFake) Handoff(ctx context.Context, caller domain.Caller, req service.HandoffTaskReq) (service.HandoffTaskResp, error) {
	return call[service.HandoffTaskResp](x.f, "Lifecycle.Handoff", caller, req)
}

func (x lifecycleFake) Cancel(ctx context.Context, caller domain.Caller, req service.CancelTaskReq) (service.CancelTaskResp, error) {
	return call[service.CancelTaskResp](x.f, "Lifecycle.Cancel", caller, req)
}

type contextFake struct{ f *Fake }

func (x contextFake) AddItem(ctx context.Context, caller domain.Caller, req service.AddContextItemReq) (service.AddContextItemResp, error) {
	return call[service.AddContextItemResp](x.f, "Context.AddItem", caller, req)
}

func (x contextFake) GetTaskContext(ctx context.Context, caller domain.Caller, req service.GetTaskContextReq) (service.GetTaskContextResp, error) {
	return call[service.GetTaskContextResp](x.f, "Context.GetTaskContext", caller, req)
}

func (x contextFake) SetSummary(ctx context.Context, caller domain.Caller, req service.SetSummaryReq) (service.SetSummaryResp, error) {
	return call[service.SetSummaryResp](x.f, "Context.SetSummary", caller, req)
}

func (x contextFake) WriteMemory(ctx context.Context, caller domain.Caller, req service.WriteMemoryReq) (service.WriteMemoryResp, error) {
	return call[service.WriteMemoryResp](x.f, "Context.WriteMemory", caller, req)
}

func (x contextFake) ReadMemory(ctx context.Context, caller domain.Caller, req service.ReadMemoryReq) (service.ReadMemoryResp, error) {
	return call[service.ReadMemoryResp](x.f, "Context.ReadMemory", caller, req)
}

func (x contextFake) RecallMemory(ctx context.Context, caller domain.Caller, req service.RecallMemoryReq) (service.RecallMemoryResp, error) {
	return call[service.RecallMemoryResp](x.f, "Context.RecallMemory", caller, req)
}

func (x contextFake) AddDependency(ctx context.Context, caller domain.Caller, req service.AddDependencyReq) (service.AddDependencyResp, error) {
	return call[service.AddDependencyResp](x.f, "Context.AddDependency", caller, req)
}

func (x contextFake) RemoveDependency(ctx context.Context, caller domain.Caller, req service.RemoveDependencyReq) (service.RemoveDependencyResp, error) {
	return call[service.RemoveDependencyResp](x.f, "Context.RemoveDependency", caller, req)
}

func (x contextFake) AddSubtask(ctx context.Context, caller domain.Caller, req service.AddSubtaskReq) (service.AddSubtaskResp, error) {
	return call[service.AddSubtaskResp](x.f, "Context.AddSubtask", caller, req)
}

func (x contextFake) ListSubtasks(ctx context.Context, caller domain.Caller, req service.ListSubtasksReq) (service.ListSubtasksResp, error) {
	return call[service.ListSubtasksResp](x.f, "Context.ListSubtasks", caller, req)
}

type mediaFake struct{ f *Fake }

func (x mediaFake) AttachFile(ctx context.Context, caller domain.Caller, req service.AttachFileReq) (service.AttachFileResp, error) {
	return call[service.AttachFileResp](x.f, "Media.AttachFile", caller, req)
}

func (x mediaFake) ListAttachments(ctx context.Context, caller domain.Caller, req service.ListAttachmentsReq) (service.ListAttachmentsResp, error) {
	return call[service.ListAttachmentsResp](x.f, "Media.ListAttachments", caller, req)
}

func (x mediaFake) SearchTasks(ctx context.Context, caller domain.Caller, req service.SearchTasksReq) (service.SearchTasksResp, error) {
	return call[service.SearchTasksResp](x.f, "Media.SearchTasks", caller, req)
}

type feedFake struct{ f *Fake }

func (x feedFake) BoardFeed(ctx context.Context, caller domain.Caller, req service.BoardFeedReq) (service.BoardFeedResp, error) {
	return call[service.BoardFeedResp](x.f, "Feed.BoardFeed", caller, req)
}

func (x feedFake) BoardSnapshot(ctx context.Context, caller domain.Caller, req service.BoardSnapshotReq) (service.BoardSnapshotResp, error) {
	return call[service.BoardSnapshotResp](x.f, "Feed.BoardSnapshot", caller, req)
}

type sessionsFake struct{ f *Fake }

func (x sessionsFake) TouchAgentSession(ctx context.Context, caller domain.Caller, req service.TouchAgentSessionReq) (service.TouchAgentSessionResp, error) {
	return call[service.TouchAgentSessionResp](x.f, "Sessions.TouchAgentSession", caller, req)
}

func (x sessionsFake) ListAgentSessions(ctx context.Context, caller domain.Caller, req service.ListAgentSessionsReq) (service.ListAgentSessionsResp, error) {
	return call[service.ListAgentSessionsResp](x.f, "Sessions.ListAgentSessions", caller, req)
}

func (x sessionsFake) RegisterWakeSession(ctx context.Context, caller domain.Caller, req service.RegisterWakeSessionReq) (service.RegisterWakeSessionResp, error) {
	return call[service.RegisterWakeSessionResp](x.f, "Sessions.RegisterWakeSession", caller, req)
}

type toolCallsFake struct{ f *Fake }

func (x toolCallsFake) Record(ctx context.Context, caller domain.Caller, req service.RecordToolCallReq) (service.RecordToolCallResp, error) {
	return call[service.RecordToolCallResp](x.f, "ToolCalls.Record", caller, req)
}

var (
	_ service.Projects  = projectsFake{}
	_ service.Boards    = boardsFake{}
	_ service.Tasks     = tasksFake{}
	_ service.Lifecycle = lifecycleFake{}
	_ service.Context   = contextFake{}
	_ service.Media     = mediaFake{}
	_ service.Feed      = feedFake{}
	_ service.Sessions  = sessionsFake{}
	_ service.ToolCalls = toolCallsFake{}
)

type chronicleFake struct{ f *Fake }

func (x chronicleFake) CreatePage(ctx context.Context, caller domain.Caller, req service.CreateChroniclePageReq) (service.CreateChroniclePageResp, error) {
	return call[service.CreateChroniclePageResp](x.f, "Chronicle.CreatePage", caller, req)
}

func (x chronicleFake) IngestPage(ctx context.Context, caller domain.Caller, req service.IngestChroniclePageReq) (service.IngestChroniclePageResp, error) {
	return call[service.IngestChroniclePageResp](x.f, "Chronicle.IngestPage", caller, req)
}

func (x chronicleFake) EditPage(ctx context.Context, caller domain.Caller, req service.EditChroniclePageReq) (service.EditChroniclePageResp, error) {
	return call[service.EditChroniclePageResp](x.f, "Chronicle.EditPage", caller, req)
}

func (x chronicleFake) DeletePage(ctx context.Context, caller domain.Caller, req service.DeleteChroniclePageReq) (service.DeleteChroniclePageResp, error) {
	return call[service.DeleteChroniclePageResp](x.f, "Chronicle.DeletePage", caller, req)
}

func (x chronicleFake) ListPages(ctx context.Context, caller domain.Caller, req service.ListChroniclePagesReq) (service.ChronicleIndex, error) {
	return call[service.ChronicleIndex](x.f, "Chronicle.ListPages", caller, req)
}

func (x chronicleFake) GetPage(ctx context.Context, caller domain.Caller, req service.GetChroniclePageReq) (service.ChroniclePageView, error) {
	return call[service.ChroniclePageView](x.f, "Chronicle.GetPage", caller, req)
}

func (x chronicleFake) ListVersions(ctx context.Context, caller domain.Caller, req service.ListChronicleVersionsReq) (service.ChronicleHistory, error) {
	return call[service.ChronicleHistory](x.f, "Chronicle.ListVersions", caller, req)
}

func (x chronicleFake) GetVersion(ctx context.Context, caller domain.Caller, req service.GetChronicleVersionReq) (service.ChronicleVersionView, error) {
	return call[service.ChronicleVersionView](x.f, "Chronicle.GetVersion", caller, req)
}

type artifactsFake struct{ f *Fake }

func (x artifactsFake) AddArtifact(ctx context.Context, caller domain.Caller, req service.AddArtifactReq) (service.AddArtifactResp, error) {
	return call[service.AddArtifactResp](x.f, "Artifacts.AddArtifact", caller, req)
}

func (x artifactsFake) RetractArtifact(ctx context.Context, caller domain.Caller, req service.RetractArtifactReq) (service.RetractArtifactResp, error) {
	return call[service.RetractArtifactResp](x.f, "Artifacts.RetractArtifact", caller, req)
}

type commentsFake struct{ f *Fake }

func (x commentsFake) AddComment(ctx context.Context, caller domain.Caller, req service.AddCommentReq) (service.AddCommentResp, error) {
	return call[service.AddCommentResp](x.f, "Comments.AddComment", caller, req)
}

func (x commentsFake) ListComments(ctx context.Context, caller domain.Caller, req service.ListCommentsReq) (service.ListCommentsResp, error) {
	return call[service.ListCommentsResp](x.f, "Comments.ListComments", caller, req)
}

type followUpsFake struct{ f *Fake }

func (x followUpsFake) AddFollowUp(ctx context.Context, caller domain.Caller, req service.AddFollowUpReq) (service.AddFollowUpResp, error) {
	return call[service.AddFollowUpResp](x.f, "FollowUps.AddFollowUp", caller, req)
}

func (x followUpsFake) AckFollowUp(ctx context.Context, caller domain.Caller, req service.AckFollowUpReq) (service.AckFollowUpResp, error) {
	return call[service.AckFollowUpResp](x.f, "FollowUps.AckFollowUp", caller, req)
}

func (x followUpsFake) ListFollowUps(ctx context.Context, caller domain.Caller, req service.ListFollowUpsReq) (service.ListFollowUpsResp, error) {
	return call[service.ListFollowUpsResp](x.f, "FollowUps.ListFollowUps", caller, req)
}
