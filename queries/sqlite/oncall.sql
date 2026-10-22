-- name: GetIntegration :one
SELECT * FROM integrations WHERE project_id = ? AND id = ?;

-- name: GetAlertState :one
SELECT * FROM alert_states WHERE project_id = ? AND integration_id = ? AND fingerprint_hash = ?;

-- name: GetDelivery :one
SELECT * FROM deliveries WHERE project_id = ? AND integration_id = ? AND id = ?;

-- name: GetDeliveryRawBody :one
SELECT * FROM delivery_raw_bodies WHERE project_id = ? AND integration_id = ? AND delivery_id = ?;
