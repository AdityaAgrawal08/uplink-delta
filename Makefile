.PHONY: build install clean

build:
	cd cli && CGO_ENABLED=0 go build -o uplink -ldflags="-s -w" main.go

install: build
	install -Dm755 cli/uplink /usr/local/bin/uplink

clean:
	rm -f cli/uplink
