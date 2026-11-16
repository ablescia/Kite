# Kite

Tastiera a sguardo ibrida per la **comunicazione aumentativa e alternativa
(CAA)**, nativa per Linux (Go puro, X11/XWayland), con il Tobii Eye Tracker 4C.

> **In English.** Kite is an open on-screen gaze keyboard for augmentative and
> alternative communication. A key fires when the gaze settles after the
> saccade that brings it there, after a time that depends on how likely the
> letter is; likely keys invisibly grow their hit areas (Bayesian decoding);
> the pupil, compensated for screen luminance, estimates cognitive load. It
> includes an Italian corrector, local neural speech (Piper) and learned
> phrases. It reads gaze data from the
> [tobii4c-linux](https://github.com/ablescia/tobii4c-linux) driver. The
> methods are described in [doi:10.5281/zenodo.24282465](https://doi.org/10.5281/zenodo.24282465).
> The documentation below is in Italian.

Con una tastiera a sguardo classica si fissa un tasto per un tempo fisso,
spesso 1 s, e il tasto si attiva. Kite fa diversamente:

- il tasto si attiva **appena lo sguardo si ferma dopo la saccade** che lo
  porta sul tasto, prima se la lettera è probabile in quel punto della
  parola, più tardi se è improbabile;
- le aree di tocco dei tasti probabili **si allargano in modo invisibile**;
- la **pupilla**, compensata per la luminanza dello schermo, stima il carico
  cognitivo e può allungare i tempi.

C'è anche un **correttore automatico italiano**, come quello dei telefoni. Allo
spazio sistema la parola: lettere in più, doppie saltate, accenti ("perche" →
"perché") ed elisioni ("l acqua" → "l'acqua"). Mentre si scrive, una barra
sopra la tastiera propone tre completamenti selezionabili con lo sguardo.

Per comunicare c'è la **voce**: il tasto **parla** fa dire il testo a una voce
italiana naturale, sintetizzata in locale con Piper (nessun dato esce dal
computer). Quando la voce ha finito, il testo si cancella ed è pronto il
messaggio successivo.

I messaggi detti si **imparano**: quando si ricomincia a scriverne uno, Kite
propone il resto della frase in grigio chiaro dopo il cursore, e fissarla per
un secondo la scrive.

La tastiera è una QWERTY a schermo di 27 tasti (26 lettere su tre righe
sfalsate e la barra spaziatrice), più la barra di modifica (tre suggerimenti,
cancella e parla). Come su una tastiera vera, gli errori entrano nel testo e
si correggono.

Kite è **sperimentale**: è stato provato al banco su una persona e con un
utente simulato, non ancora con chi ne ha bisogno.
Il percorso è raccontato nel diario di ricerca
[SmartChair](https://smartchair.it/ricerche/kite/).

## Che cosa serve

- Linux con X11, oppure Wayland con XWayland;
- un **Tobii Eye Tracker 4C** e il driver
  **[tobii4c-linux](https://github.com/ablescia/tobii4c-linux)**, che legge il
  tracker e manda i dati a Kite (vedi [Il driver](#il-driver-tobii4c-linux));
- Go ≥ 1.22 per compilare;
- per la voce, facoltativa: [Piper](https://github.com/OHF-Voice/piper1-gpl)
  e una voce italiana (vedi l'avvio rapido).

Senza tracker Kite funziona con un utente simulato (`-simulate`), utile per
provare l'interfaccia.

## Avvio rapido

```sh
# 1. il driver, una volta sola (dettagli e dipendenze nel suo README)
git clone https://github.com/ablescia/tobii4c-linux && cd tobii4c-linux
make install                     # → ~/.local/bin/tobii4c
make install-udev                # accesso al tracker senza root
cd ..

# 2. Kite
git clone https://github.com/ablescia/kite && cd kite
make build                       # → bin/kite e bin/kite-replay

# 3. la voce, una volta sola: Piper e la voce italiana Serena
uv tool install piper-tts
mkdir -p ~/.local/share/piper/voices && cd ~/.local/share/piper/voices
for f in it_IT-serena-high.onnx it_IT-serena-high.onnx.json; do
  curl -fLO "https://huggingface.co/rhasspy/piper-voices/resolve/main/it/it_IT/serena/high/$f"
done
cd - && ./bin/kite -say "ciao, come stai?"   # prova dell'audio

# 4. terminale 1: il driver. Il pitch della testa qui non serve: -headpose off
#    risparmia CPU ed evita i rari frame persi con lo stream immagine.
tobii4c run -headpose off

# 5. terminale 2
./bin/kite
```

Al primo avvio, dal menu: **1** calibrazione dello sguardo, **2** calibrazione
pupilla–luminanza, poi **3** per scrivere. La riga di stato del menu dice se i
dati arrivano e se gli occhi sono tracciati; la distanza giusta è 50–80 cm.

Per l'uso di tutti i giorni conviene far girare il driver come **servizio
utente di systemd** (vedi il README del driver): parte da solo con la
sessione, e Kite lo ferma e lo riavvia da sé durante la calibrazione dello
sguardo.

Senza tracker: `./bin/kite -simulate`. Senza finestra: `make demo` esegue
l'esercizio con l'utente simulato in headless e salva gli screenshot in
`/tmp/kite-demo`.

## Il driver: tobii4c-linux

Kite non tocca il tracker: legge soltanto. I dati arrivano dal driver
[tobii4c-linux](https://github.com/ablescia/tobii4c-linux), in Go puro, che
parla con il 4C via USB senza binari proprietari di Tobii e trasmette i
campioni in **UDP, formato T4CG v2, a 90 Hz**, di default su
`127.0.0.1:5005`. Kite usa:

- lo sguardo filtrato dal firmware (stream 0x1c) e quello non filtrato
  (0x20): le saccadi si cercano su quello non filtrato, che non ha i ~30 ms
  di ritardo del filtro;
- i diametri delle pupille;
- i centri corneali, cioè la posizione degli occhi nel sistema dello
  schermo, per convertire lo sguardo in gradi di angolo visivo;
- la presenza della persona.

Il driver si avvia in due modi:

- **a mano**, in un terminale: `tobii4c run -headpose off`;
- **come servizio** utente di systemd (`tobii4c.service`, installato come
  spiega il README del driver), che trasmette sullo stesso indirizzo.

Se Kite ascolta su un altro indirizzo (`-udp`), il driver va avviato con lo
stesso (`tobii4c run -udp …`). Dal driver Kite prende anche la **geometria
dello schermo**, cioè le dimensioni in millimetri dell'area su cui si guarda:
la scrive il primo passo della calibrazione dello sguardo in
`~/.config/tobii4c/display.json`. Se manca, Kite usa le dimensioni dichiarate
dal monitor (EDID) o `-display-mm`.

## Le calibrazioni

Servono due calibrazioni, e si fanno entrambe dal menu di Kite.

### 1. Calibrazione dello sguardo (tasto 1)

Adatta il modello degli occhi del tracker alla persona. La fa **chi userà
Kite**, con i propri occhi, e va rifatta quando cambiano la persona, il
monitor o la posizione del sensore, o con occhiali o lenti a contatto
diversi.

La esegue il driver (`tobii4c calibrate`), nella sua finestra a schermo
intero, con la stessa sequenza dell'applicazione ufficiale di Tobii:

1. posizione del sensore sotto lo schermo, che dà anche la geometria dello
   schermo;
2. posizione della persona (distanza 50–80 cm);
3. 7 punti da fissare;
4. verifica su 5 punti diversi, con l'accuratezza in gradi.

Basta guardare i punti; i tasti li può premere chi assiste. Il driver salva
la calibrazione in `~/.config/tobii4c/calibration.bin`. Prima di cominciare
fa un backup di quella attiva, e con Esc la rimette.

Il driver vuole il tracker in esclusiva, quindi quello che sta trasmettendo
va fermato. Prima di partire Kite guarda chi tiene aperto il tracker:

- **il servizio** `tobii4c.service` (o un altro servizio utente): Kite lo
  ferma, lancia la calibrazione e poi lo riavvia, anche se la calibrazione
  non riesce;
- **un driver avviato a mano** in un terminale: Kite non lo chiude. Dice quale
  processo è (PID e riga di comando) e aspetta che venga chiuso con Ctrl+C;
  appena il tracker è libero, la calibrazione si può avviare. Dopo, il driver
  va riavviato a mano.

Durante la calibrazione Kite nasconde la propria finestra e lascia tastiera e
schermo al driver, sullo stesso monitor. Alla fine riprende lo schermo e
mostra:

- com'è finita: salvata, interrotta, o non riuscita con l'errore del driver;
- l'accuratezza e la precisione medie della verifica;
- se il servizio è stato riavviato.

Con il 4C il driver misura di solito un'accuratezza di 0,6–0,9°. Oltre 1,2°
Kite consiglia di ripetere la calibrazione. Il motivo: sulla QWERTY di uno
schermo di 700 × 390 mm visto da 62 cm, la posteriore del tasto guardato
scende sotto 0,95 a 1,85–2,15° dal centro verso i vicini, e lì il selettore
allunga i tempi per prudenza. Lo sguardo finale si disperde già di ~0,65°
attorno al punto mirato, quindi con uno scarto sistematico oltre ~1,2° molte
fissazioni finiscono oltre quel limite.

Se la calibrazione ha misurato uno schermo di dimensioni diverse da quelle in
uso, Kite si riavvia per usarle (Invio), con una sessione nuova. Non succede
se le dimensioni sono date con `-display-mm`. L'output del driver si salva
nella cartella della sessione (`gaze-calibration-<ora>.log`). Il driver si
cerca nel `PATH`, poi in `~/.local/bin/tobii4c`; `-tobii4c` ne indica un altro.

Con l'utente simulato non c'è un tracker da calibrare, e Kite non lancia il
driver.

### 2. Calibrazione pupilla–luminanza (tasto 2)

Misura quanto la pupilla di chi scrive risponde alla luce di questo monitor e
di questa stanza, per separarla poi dal carico cognitivo. Si guarda una croce
per circa 30 s mentre lo sfondo passa per 9 livelli di grigio. Il risultato
mostra il modello stimato, la curva prevista sopra quella misurata e l'R². Con
R² < 0,6 la compensazione è segnalata come incerta.

Il modello vale 12 ore, perché la luce della stanza cambia nel corso della
giornata, e si salva in `~/.config/kite/pupil-model.json`. Senza modello la
tastiera funziona, ma la pupilla non modula i tempi. L'esercizio di
copiatura, se il modello manca, comincia da questa calibrazione.

## Licenza

Kite è distribuito con la [GNU AGPL v3 o successiva](LICENSE). Fanno eccezione `internal/x11ui`, copiato dal
driver con licenza [MIT](internal/x11ui/LICENSE), e il lessico `internal/lm/data/it_150k.txt.gz`, con licenza
CC BY-SA 4.0 ([LEGGIMI.md](internal/lm/data/LEGGIMI.md)).
