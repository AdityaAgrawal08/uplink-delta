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
	# Linux AMD64
	cd cli && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/uplink -ldflags="-s -w" main.go
	cd cli/build && tar -czf uplink-linux-amd64.tar.gz uplink && rm uplink
	# Linux ARM64
	cd cli && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/uplink -ldflags="-s -w" main.go
	cd cli/build && tar -czf uplink-linux-arm64.tar.gz uplink && rm uplink
	# Windows AMD64
	cd cli && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/uplink.exe -ldflags="-s -w" main.go
	cd cli/build && tar -czf uplink-windows-amd64.tar.gz uplink.exe && rm uplink.exe
	# Darwin AMD64
	cd cli && GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o build/uplink -ldflags="-s -w" main.go
	cd cli/build && tar -czf uplink-darwin-amd64.tar.gz uplink && rm uplink
	# Darwin ARM64
	cd cli && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o build/uplink -ldflags="-s -w" main.go
	cd cli/build && tar -czf uplink-darwin-arm64.tar.gz uplink && rm uplink
