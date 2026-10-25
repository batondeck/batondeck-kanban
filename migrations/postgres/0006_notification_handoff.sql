-- +goose Up
-- P-275 (audit A12), owner-approved 2026-10-24 (zone Z2): a handoff writes the receiver a
-- notification, so a client that pulls (list_notifications each turn) learns of it. The type CHECK
-- gains 'handoff'. Down drops the handoff rows, which the old CHECK cannot hold.
ALTER TABLE notifications DROP CONSTRAINT notifications_type_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_type_check CHECK (type IN ('mention', 'follow_up', 'review_request', 'handoff'));

-- +goose Down
DELETE FROM notifications WHERE type = 'handoff';
ALTER TABLE notifications DROP CONSTRAINT notifications_type_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_type_check CHECK (type IN ('mention', 'follow_up', 'review_request'));
