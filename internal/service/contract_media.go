package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Media is attachments and search.
type Media interface {
	// AttachFile is attach_file.
	// Tier: write. Event: attachment.created. Audit: none. Doorbell: none. Replays on idempotencyKey.
	AttachFile(ctx context.Context, caller domain.Caller, req AttachFileReq) (AttachFileResp, error)

	// ListAttachments is list_attachments.
	// Tier: read. Writes nothing.
	ListAttachments(ctx context.Context, caller domain.Caller, req ListAttachmentsReq) (ListAttachmentsResp, error)

	// SearchTasks is search_tasks.
	// Tier: read. Writes nothing.
	SearchTasks(ctx context.Context, caller domain.Caller, req SearchTasksReq) (SearchTasksResp, error)
}

// AttachFileReq is attach_file's input.
type AttachFileReq struct {
	Bytes          int64     `json:"bytes"`
	FileName       string    `json:"fileName"`
	Kind           MediaKind `json:"kind"`
	MimeType       string    `json:"mimeType"`
	ProjectID      string    `json:"projectId"`
	TaskID         string    `json:"taskId"`
	IdempotencyKey string    `json:"idempotencyKey,omitzero"`
}

// AttachFileResp is attach_file's output.
type AttachFileResp struct {
	AttachmentID string `json:"attachmentId"`
	ExpiresAt    string `json:"expiresAt"`
	UploadURL    string `json:"uploadUrl"`
}

// ListAttachmentsReq is list_attachments's input.
type ListAttachmentsReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
}

// ListAttachmentsResp is list_attachments's output.
type ListAttachmentsResp struct {
	Attachments []Attachment `json:"attachments"`
}

// SearchTasksReq is search_tasks's input.
type SearchTasksReq struct {
	ProjectID string     `json:"projectId"`
	Query     string     `json:"query"`
	BoardID   string     `json:"boardId,omitzero"`
	K         int64      `json:"k,omitzero"`
	Mode      SearchMode `json:"mode,omitzero"`
}

// SearchTasksResp is search_tasks's output.
type SearchTasksResp struct {
	Mode    SearchMode     `json:"mode"`
	Results []SearchResult `json:"results"`
}

// Attachment is list_attachments's output attachments[], first seen there.
type Attachment struct {
	Bytes         float64          `json:"bytes"`
	CreatedBy     string           `json:"createdBy"`
	DownloadURL   *string          `json:"downloadUrl"`
	ExtractedText *string          `json:"extractedText"`
	FileName      string           `json:"fileName"`
	GCSURI        string           `json:"gcsUri"`
	ID            string           `json:"id"`
	Kind          MediaKind        `json:"kind"`
	MimeType      string           `json:"mimeType"`
	SHA256        *string          `json:"sha256"`
	Status        AttachmentStatus `json:"status"`
	TaskID        string           `json:"taskId"`
	ThumbURI      *string          `json:"thumbUri"`
	Ts            string           `json:"ts"`
}

// SearchResult is search_tasks's output results[], first seen there.
type SearchResult struct {
	Score float64 `json:"score"`
	Task  Task    `json:"task"`
}

// SearchMode is a search_tasks mode.
type SearchMode string

// SearchModeValues are SearchMode's values, in the schema's order.
var SearchModeValues = []SearchMode{"hybrid", "keyword", "semantic"}
