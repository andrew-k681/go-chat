module github.com/andrew-k681/go-chat/server

go 1.27

require (
	github.com/gorilla/websocket v1.5.3
	github.com/twmb/franz-go v1.22.1
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20260927204940-b5a45ccfdf7e
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.14.0 // indirect
)

tool google.golang.org/protobuf/cmd/protoc-gen-go
