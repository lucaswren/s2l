.PHONY: build frontend run test clean install-service

frontend:
	cd web && npm ci && npm run build

build: frontend
	go build -o bin/s2l ./cmd/s2l

run: build
	sudo ./bin/s2l -config config.example.json

test:
	go test ./...

clean:
	rm -rf bin/ web/node_modules/ script/__pycache__/
	rm -f s2l.exe

install-service:
	sudo install -d /opt/s2l
	sudo install -m 755 bin/s2l /opt/s2l/s2l
	sudo install -m 755 script/s2l.sh /usr/local/bin/s2l
	sudo install -m 600 script/manage_config.py /opt/s2l/manage_config.py
	sudo install -m 600 script/manage_ssh.py /opt/s2l/manage_ssh.py
	sudo install -m 644 config.example.json /opt/s2l/config.json
	sudo install -m 644 deploy/systemd/s2l.service /etc/systemd/system/s2l.service
	sudo systemctl daemon-reload
	sudo systemctl enable --now s2l
