package mcp_test

import "github.com/batondeck/batondeck-kanban/internal/service"

// mediaCases are attach_file and list_attachments for TestProjectsBoardsTools.
func mediaCases() []toolCase {
	url := "http://127.0.0.1:8080/blobs/abc?exp=1&sig=00"
	sha := "ab"
	return []toolCase{
		{tool: "attach_file", method: "Media.AttachFile", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "fileName": "a.txt", "mimeType": "text/plain", "kind": "file", "bytes": 5, "idempotencyKey": "k-1"},
			want: service.AttachFileReq{ProjectID: "P-1", TaskID: "T-1", FileName: "a.txt", MimeType: "text/plain", Kind: "file", Bytes: 5, IdempotencyKey: "k-1"},
			resp: service.AttachFileResp{AttachmentID: "A-1", ExpiresAt: "2026-10-23T00:15:00.000Z", UploadURL: url}},
		{tool: "list_attachments", method: "Media.ListAttachments", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1"},
			want: service.ListAttachmentsReq{ProjectID: "P-1", TaskID: "T-1"},
			resp: service.ListAttachmentsResp{Attachments: []service.Attachment{{
				Bytes: 5, CreatedBy: "u-ada", DownloadURL: &url, GCSURI: "blob:P-1.T-1.A-1", ID: "A-1", Kind: "file",
				MimeType: "text/plain", SHA256: &sha, Status: "ready", TaskID: "T-1", Ts: "2026-10-23T00:00:00.000Z",
			}}}},
	}
}
