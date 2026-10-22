-- name: GetIntegration :one
SELECT * FROM integrations WHERE project_id = $1 AND id = $2;

-- name: GetDelivery :one
SELECT * FROM deliveries WHERE project_id = $1 AND integration_id = $2 AND id = $3;

-- name: GetDeliveryRawBody :one
SELECT * FROM delivery_raw_bodies WHERE project_id = $1 AND integration_id = $2 AND delivery_id = $3;
