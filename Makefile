.PHONY: build install clean release

build:
	cd cli && CGO_ENABLED=0 go build -o uplink -ldflags="-s -w" main.go

install: build
	install -Dm755 cli/uplink /usr/local/bin/uplink

clean:
	rm -f cli/uplink
	rm -rf cli/build

release:
	mkdir -p cli/build
	cd cli && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/uplink-linux-amd64 -ldflags="-s -w" main.go
	cd cli && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/uplink-linux-arm64 -ldflags="-s -w" main.go
	cd cli && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/uplink-windows-amd64.exe -ldflags="-s -w" main.go
	cd cli && GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o build/uplink-darwin-amd64 -ldflags="-s -w" main.go
	cd cli && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o build/uplink-darwin-arm64 -ldflags="-s -w" main.go
