bu:
	git pull
	cd tools/goctl && go build
	mv tools/goctl/goctl $(GOPATH)/bin