go_build := "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build --trimpath --ldflags '-w -s'"
image_name := "iptv-scraper"

default: build

build:
	mkdir -p build
	{{go_build}} -o build/iptv-scraper .

docker:
	docker build -t {{image_name}} .