.PHONY: build build-go frontend run test clean install-service

frontend:
	cd web && npm ci && npm run build

build: frontend
	$(MAKE) build-go

build-go:
	@test -f web/dist/index.html || { echo "Frontend assets missing. Run make frontend first."; exit 1; }
	go build -o bin/s2l ./cmd/s2l

run: build-go
	sudo ./bin/s2l -config config.example.json

test:
	go test ./...

clean:
	rm -rf bin/ web/node_modules/ script/__pycache__/
	rm -f s2l.exe

install-service:
	sudo bash script/install.sh
