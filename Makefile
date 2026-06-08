APP ?= wikilite
BUILD_DIR = build

.PHONY: clean lint test app all static cgo

all: lint app

clean:
	@rm -rf $(BUILD_DIR)
	@rm -f android/app/src/main/jniLibs/arm64-v8a/lib$(APP).so
	@rm -f android/app/src/main/jniLibs/armeabi-v7a/lib$(APP).so

lint:
	@gofmt -w ./app

test:
	@go test -v ./test

app:
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags "-s -w" -o $(BUILD_DIR)/$(APP) ./app
	@GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o android/app/src/main/jniLibs/arm64-v8a/lib$(APP).so ./app
	@GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "-s -w" -o android/app/src/main/jniLibs/armeabi-v7a/lib$(APP).so ./app

static:
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 go build -ldflags "-s -w" -o $(BUILD_DIR)/$(APP) ./app

