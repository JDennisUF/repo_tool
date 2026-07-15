BINARY := rt

.PHONY: build clean

build:
	./build.sh $(BINARY)

clean:
	rm -f $(BINARY)
