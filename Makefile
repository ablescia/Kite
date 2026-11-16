BIN := bin/kite

.PHONY: build test race run sim demo replay install clean

build:
	go build -trimpath -o $(BIN) ./cmd/kite
	go build -trimpath -o bin/kite-replay ./cmd/replay

test:
	go vet ./... && go test ./...

race:
	go test -race ./...

# Con il tracker: il driver tobii4c deve trasmettere («tobii4c run -headpose off»
# in un altro terminale, oppure il servizio utente tobii4c).
run: build
	./$(BIN)

# Utente simulato, a schermo intero.
sim: build
	./$(BIN) -simulate

# Esercizio simulato senza finestra (≈1 minuto), screenshot in /tmp/kite-demo.
demo: build
	./$(BIN) -simulate -headless -auto -words 3 -shots /tmp/kite-demo/shots -out /tmp/kite-demo/session

# Rigioca le sessioni registrate: parametri registrati contro attuali.
replay: build
	./bin/kite-replay

PREFIX ?= $(HOME)/.local

install: build
	install -Dm755 $(BIN) $(PREFIX)/bin/kite
	install -Dm755 bin/kite-replay $(PREFIX)/bin/kite-replay

clean:
	rm -rf bin
