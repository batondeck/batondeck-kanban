-- name: GetIntegration :one
SELECT * FROM integrations WHERE project_id = ? AND id = ?;

-- name: GetDelivery :one
SELECT * FROM deliveries WHERE project_id = ? AND integration_id = ? AND id = ?;

-- name: GetDeliveryRawBody :one
SELECT * FROM delivery_raw_bodies WHERE project_id = ? AND integration_id = ? AND delivery_id = ?;
