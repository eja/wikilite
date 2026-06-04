.PHONY: clean lint test app all

all: lint app

clean:
	@rm -rf build

lint:
	@gofmt -w ./app

test:
	@CGO_ENABLED=1 go test -tags "fts5" -v ./test

app:
	@mkdir -p build
	@CGO_ENABLED=1 go build -tags "fts5" -ldflags "-s -w" -o build/wikilite ./app
