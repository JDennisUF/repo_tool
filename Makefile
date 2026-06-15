BINARY := rt

.PHONY: build clean

build:
	go build -o $(BINARY) ./cmd/rt

clean:
	rm -f $(BINARY)
