PROTO_DIR=proto
GEN_DIR=gen
SWAGGER_DIR=docs/swagger
GOOGLEAPIS_DIR=third_party/googleapis

proto:
	protoc -I . -I $(GOOGLEAPIS_DIR) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=$(GEN_DIR) --grpc-gateway_opt=paths=source_relative \
		--openapiv2_out=$(SWAGGER_DIR) \
		$(PROTO_DIR)/blog/v1/blog.proto

run:
	go run ./cmd/blog

tidy:
	go mod tidy
