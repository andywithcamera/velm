CREATE EXTENSION IF NOT EXISTS pgcrypto;

INSERT INTO base_entity (
	_id,
	number,
	name,
	description,
	entity_type,
	lifecycle_state,
	operational_status,
	criticality
)
VALUES
	('90000000-0000-0000-0000-000000000101', 'ENT-900101', 'Order Platform Service', 'Customer-facing ordering service that coordinates checkout, payment, and account lookups.', 'service', 'active', 'operational', 'p1'),
	('90000000-0000-0000-0000-000000000102', 'ENT-900102', 'Checkout API', 'Handles cart validation and checkout orchestration for the order platform.', 'application', 'active', 'operational', 'p1'),
	('90000000-0000-0000-0000-000000000103', 'ENT-900103', 'Payment Orchestrator', 'Coordinates payment authorization and settlement calls for completed orders.', 'application', 'active', 'operational', 'p1'),
	('90000000-0000-0000-0000-000000000104', 'ENT-900104', 'Customer Profile API', 'Provides account and saved-profile data during checkout.', 'application', 'active', 'operational', 'p2'),
	('90000000-0000-0000-0000-000000000105', 'ENT-900105', 'Orders PostgreSQL Cluster', 'Primary transactional data store for orders and fulfillment state.', 'database', 'active', 'operational', 'p1'),
	('90000000-0000-0000-0000-000000000106', 'ENT-900106', 'Orders Redis Cache', 'Low-latency cache for active sessions, cart fragments, and pricing hints.', 'cache', 'active', 'operational', 'p2'),
	('90000000-0000-0000-0000-000000000107', 'ENT-900107', 'Order Events Stream', 'Kafka-backed stream that distributes order lifecycle events downstream.', 'messaging', 'active', 'operational', 'p2'),
	('90000000-0000-0000-0000-000000000108', 'ENT-900108', 'Production Kubernetes Cluster', 'Shared production cluster that runs the order platform workloads.', 'cluster', 'active', 'operational', 'p1'),
	('90000000-0000-0000-0000-000000000109', 'ENT-900109', 'Synthetic Checkout Monitor', 'External synthetic test that continuously probes the public checkout journey.', 'monitor', 'active', 'operational', 'p3'),
	('90000000-0000-0000-0000-000000000110', 'ENT-900110', 'Order Platform Incident Service', 'Operational escalation service used for paging and incident routing.', 'support_service', 'active', 'operational', 'p2')
ON CONFLICT (_id) DO UPDATE
SET number = EXCLUDED.number,
	name = EXCLUDED.name,
	description = EXCLUDED.description,
	entity_type = EXCLUDED.entity_type,
	lifecycle_state = EXCLUDED.lifecycle_state,
	operational_status = EXCLUDED.operational_status,
	criticality = EXCLUDED.criticality,
	_deleted_at = NULL,
	_updated_at = NOW();

INSERT INTO base_entity_relationship (
	_id,
	source_entity_id,
	target_entity_id,
	relationship_type,
	status,
	description
)
VALUES
	('90000000-0000-0000-0000-000000000201', '90000000-0000-0000-0000-000000000109', '90000000-0000-0000-0000-000000000101', 'monitors', 'active', 'Synthetic monitor validates the public checkout journey against the service.'),
	('90000000-0000-0000-0000-000000000202', '90000000-0000-0000-0000-000000000110', '90000000-0000-0000-0000-000000000101', 'alerts_for', 'active', 'Incident routing and paging are anchored to the service.'),
	('90000000-0000-0000-0000-000000000203', '90000000-0000-0000-0000-000000000101', '90000000-0000-0000-0000-000000000102', 'depends_on', 'active', 'The core service depends on the checkout API for request handling.'),
	('90000000-0000-0000-0000-000000000204', '90000000-0000-0000-0000-000000000101', '90000000-0000-0000-0000-000000000103', 'depends_on', 'active', 'Order completion depends on payment orchestration.'),
	('90000000-0000-0000-0000-000000000205', '90000000-0000-0000-0000-000000000101', '90000000-0000-0000-0000-000000000104', 'depends_on', 'active', 'Saved-customer context is sourced from the profile API.'),
	('90000000-0000-0000-0000-000000000206', '90000000-0000-0000-0000-000000000102', '90000000-0000-0000-0000-000000000105', 'reads_from', 'active', 'Checkout pulls order and pricing state from PostgreSQL.'),
	('90000000-0000-0000-0000-000000000207', '90000000-0000-0000-0000-000000000102', '90000000-0000-0000-0000-000000000106', 'caches_in', 'active', 'Session and cart fragments are cached in Redis.'),
	('90000000-0000-0000-0000-000000000208', '90000000-0000-0000-0000-000000000103', '90000000-0000-0000-0000-000000000105', 'writes_to', 'active', 'Payment outcomes are persisted in the orders database.'),
	('90000000-0000-0000-0000-000000000209', '90000000-0000-0000-0000-000000000103', '90000000-0000-0000-0000-000000000107', 'publishes_to', 'active', 'Payment and order lifecycle events are published to the event stream.'),
	('90000000-0000-0000-0000-000000000210', '90000000-0000-0000-0000-000000000102', '90000000-0000-0000-0000-000000000108', 'runs_on', 'active', 'Checkout API workloads run on the production Kubernetes cluster.'),
	('90000000-0000-0000-0000-000000000211', '90000000-0000-0000-0000-000000000103', '90000000-0000-0000-0000-000000000108', 'runs_on', 'active', 'Payment orchestration workloads run on the production Kubernetes cluster.'),
	('90000000-0000-0000-0000-000000000212', '90000000-0000-0000-0000-000000000104', '90000000-0000-0000-0000-000000000108', 'runs_on', 'active', 'Customer profile workloads run on the production Kubernetes cluster.'),
	('90000000-0000-0000-0000-000000000213', '90000000-0000-0000-0000-000000000104', '90000000-0000-0000-0000-000000000105', 'reads_from', 'active', 'Customer profile reads are backed by the primary orders database.')
ON CONFLICT (_id) DO UPDATE
SET source_entity_id = EXCLUDED.source_entity_id,
	target_entity_id = EXCLUDED.target_entity_id,
	relationship_type = EXCLUDED.relationship_type,
	status = EXCLUDED.status,
	description = EXCLUDED.description,
	_deleted_at = NULL,
	_updated_at = NOW();
