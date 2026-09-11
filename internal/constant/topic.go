package constant

const (
	TopicAudit = "audit"
	// TopicNotificationTransactional carries notification.created events for
	// the notification service's transactional lane (see
	// docs/reference/kafka-contract.md). The outbox worker publishes whatever
	// topic an outbox row carries, so the lane is chosen at Emit time.
	TopicNotificationTransactional = "notification.transactional"
)
