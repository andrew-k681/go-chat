BUF := go run github.com/bufbuild/buf/cmd/buf@v1.73.0

.PHONY: generate lint breaking test kafka-up kafka-down kafka-topics kafka-groups

# Regenerate Go code from proto/ into server/gen/.
generate:
	$(BUF) generate

lint:
	$(BUF) lint

# Fail if the schema changed incompatibly compared to main.
breaking:
	$(BUF) breaking --against '.git#branch=main'

test:
	cd server && go test -race ./...

# Local Apache Kafka (KRaft) + Kafka UI at http://localhost:8088.
kafka-up:
	docker compose up -d

kafka-down:
	docker compose down

# Inspect the topic with Kafka's own CLI tools.
kafka-topics:
	docker compose exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --describe --topic room-events

kafka-groups:
	docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --all-groups
