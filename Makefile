BINARY := rt

.PHONY: build clean

build:
	go build -o $(BINARY) ./cmd/repotui

clean:
	rm -f $(BINARY)
