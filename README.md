# Kite

Tastiera a sguardo ibrida per la **comunicazione aumentativa e alternativa
(CAA)**, nativa per Linux (Go puro, X11/XWayland), con il Tobii Eye Tracker 4C
e il driver [tobii4c-linux](https://github.com/ablescia/tobii4c-linux).

> **In English.** Kite is an open on-screen gaze keyboard for augmentative and
> alternative communication. A key fires when the gaze settles after the
> saccade that brings it there, after a time that depends on how likely the
> letter is; likely keys invisibly grow their hit areas (Bayesian decoding);
> the pupil, compensated for screen luminance, estimates cognitive load. It
> includes an Italian corrector, local neural speech (Piper) and learned
> phrases. It requires the
> [tobii4c-linux](https://github.com/ablescia/tobii4c-linux) user-space
> driver for the Tobii Eye Tracker 4C, which Kite reads over local UDP;
> without it, Kite only runs with a simulated user. The methods are described
> in [doi:10.5281/zenodo.24282465](https://doi.org/10.5281/zenodo.24282465).
> Kite is experimental research software, not a certified medical device; it
> is not affiliated with Tobii. License: AGPL-3.0-or-later.
> The documentation below is in Italian.

Con una tastiera a sguardo classica si fissa un tasto per un tempo fisso,
spesso 1 s, e il tasto si attiva. Kite fa diversamente:

- il tasto si attiva **appena lo sguardo si ferma dopo la saccade** che lo
  porta sul tasto, prima se la lettera è probabile in quel punto della
  parola (0,2 s), più tardi se è improbabile (fino a 1 s);
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

> **Avvertenze.** Kite è un software di ricerca e **non è un dispositivo
> medico certificato**. È fornito senza garanzie, come previsto dalla
> [licenza](#licenza). Tobii è un marchio di Tobii AB: Kite e tobii4c-linux
> sono progetti indipendenti, non affiliati, sponsorizzati né approvati da
> Tobii.

## Che cosa serve

- Linux con X11, oppure Wayland con XWayland;
- un **Tobii Eye Tracker 4C**;
- il driver **[tobii4c-linux](https://github.com/ablescia/tobii4c-linux)**,
  **indispensabile**: è lui che legge il tracker via USB e manda i dati a Kite
  (vedi [Il driver](#il-driver-tobii4c-linux)). Per compilarlo servono anche
  libusb-1.0 con i suoi header, gcc e pkg-config; a runtime udev e, per il
  servizio, systemd;
- Go ≥ 1.22 per compilare Kite e il driver;
- per la voce, facoltativa: [Piper](https://github.com/OHF-Voice/piper1-gpl)
  e una voce italiana (vedi l'avvio rapido).

Senza tracker Kite funziona solo con un utente simulato (`-simulate`), utile
per provare l'interfaccia.

## Avvio rapido

```sh
# 1. il driver tobii4c-linux, una volta sola (dettagli nel suo README).
#    Dipendenze su Debian/Ubuntu; su Arch/Omarchy:
#    sudo pacman -S --needed git base-devel go libusb pkgconf
sudo apt install git make golang libusb-1.0-0-dev pkg-config
git clone https://github.com/ablescia/tobii4c-linux && cd tobii4c-linux
make install install-udev install-service   # binario, regola udev, servizio utente
cd ..

# 2. Kite
git clone https://github.com/ablescia/Kite && cd Kite
make build                       # → bin/kite e bin/kite-replay

# 3. la voce, una volta sola: Piper e la voce italiana Serena
uv tool install piper-tts
mkdir -p ~/.local/share/piper/voices && cd ~/.local/share/piper/voices
for f in it_IT-serena-high.onnx it_IT-serena-high.onnx.json; do
  curl -fLO "https://huggingface.co/rhasspy/piper-voices/resolve/main/it/it_IT/serena/high/$f"
done
cd - && ./bin/kite -say "ciao, come stai?"   # prova dell'audio

# 4. il driver deve trasmettere prima di avviare Kite. Come servizio utente
#    (consigliato: parte da solo a ogni login)…
systemctl --user enable --now tobii4c
#    …oppure a mano, in un altro terminale. Il pitch della testa a Kite non
#    serve: -headpose off risparmia CPU ed evita i rari frame persi con lo
#    stream immagine; «saved» riapplica calibrazione e geometria dello schermo,
#    che il 4C dimentica quando resta senza alimentazione.
# tobii4c run -headpose off -display-mm saved -calibration saved

# 5. Kite
./bin/kite
```

Al primo avvio, dal menu: **1** calibrazione dello sguardo, **2** calibrazione
pupilla–luminanza, poi **3** per scrivere. La riga di stato del menu dice se i
dati arrivano e se gli occhi sono tracciati; la distanza giusta è 50–80 cm.

Per l'uso di tutti i giorni conviene il **servizio utente di systemd**: parte
da solo con la sessione, e Kite lo ferma e lo riavvia da sé durante la
calibrazione dello sguardo. Come togliergli il pitch della testa è spiegato
in [Il driver](#il-driver-tobii4c-linux).

Senza tracker: `./bin/kite -simulate`. Senza finestra: `make demo` esegue
l'esercizio con l'utente simulato in headless e salva gli screenshot in
`/tmp/kite-demo`.

## I tempi e le protezioni contro gli scatti involontari

Con lo sguardo non c'è un modo di «non premere»: ogni occhiata a un tasto è
una pressione possibile. Kite usa tempi brevi per le lettere probabili, ma
non così brevi da scrivere ciò che si guarda solo per cercarlo. Una normale
fissazione di ricerca dura 0,2–0,3 s, e per reagire al cerchio di
avanzamento e distogliere lo sguardo ne servono circa 0,2.

**Velocità** (tasto **V** del menu, oppure `-speed`). La scelta resta per le
sessioni successive (`~/.config/kite/impostazioni.json`).

| preset | lettera probabile | lettera improbabile |
|---|---|---|
| veloce | 0,2 s | 0,8 s |
| normale (predefinito) | 0,2 s | 1 s |
| prudente | 0,25 s | 1,1 s |

`-tmin` e `-tmax`, se indicati, sostituiscono il preset. Rigiocando le
sessioni registrate a novembre 2026 (`kite-replay`, sotto), il preset normale
porta le lettere poi cancellate nella scrittura libera da 76 a 30 su circa
800, rispetto ai tempi di prima (0,12–0,7 s). Costa circa 0,1 s per lettera,
ma un errore costa in mediana 3,9 s tra vederlo e cancellarlo.

**Protezioni** (sempre attive):

- **armamento**: quando la tastiera compare, o torna attiva a voce finita, lo
  sguardo che è già fermo su un tasto non lo scrive subito. Il tempo si conta
  da quell'istante e la prima selezione chiede almeno 0,5 s;
- **tasti di modifica a tempo proprio**: cancella e i suggerimenti 0,7 s,
  parla 1 s (`-edit`, `-speak`), indipendenti dalla velocità delle lettere.
  Cancella tenuto con lo sguardo si ripete ogni secondo;
- **doppie**: una doppia probabile («gg» in «oggi») si scrive tenendo il
  tasto; una improbabile («bb») no, perché lo sguardo fermo è quasi sempre
  una pausa per pensare: si esce dal tasto e si rientra. Il correttore
  ripristina comunque le doppie saltate;
- **mira della saccade**: se la saccade puntava chiaramente a un altro tasto
  e lo sguardo è finito su una lettera per strada, o nell'area allargata di
  una lettera probabile, quella lettera chiede il tempo massimo;
- **sguardo che scivola**: se l'occhio scivola lentamente senza una saccade,
  il conteggio riparte da capo e il cerchio torna vuoto, invece di restare
  pieno senza poter scattare. Su cancella, i suggerimenti e parla conta il
  punto in cui lo sguardo si è fermato adesso.

`-edit-margin 0.4` (sperimentale, spento per default) fa accettare ai tasti
di modifica anche lo sguardo fino a 0,4° fuori dal bordo: con uno scarto
verticale della calibrazione, lo sguardo su cancella cade spesso appena sopra
il tasto.

Nella diagnostica (tasto **D**) la riga in alto dice perché il tasto guardato
non scatta ancora (`attesa: …`), e lo stesso motivo finisce nella colonna
`wait` di `samples.csv`.

**Verificare una modifica sui dati reali**: `kite-replay` rigioca tutte le
sessioni registrate in `~/.local/share/kite/sessions`, scrittura libera
compresa (le lettere poi cancellate contano come errori), con i parametri di
allora e con quelli attuali; `kite-replay -suite` mostra il contributo di ogni
correzione. `-sessions` indica altre cartelle, separate da virgole.

## Il driver: tobii4c-linux

Kite non tocca il tracker: legge soltanto. Il driver
[tobii4c-linux](https://github.com/ablescia/tobii4c-linux) è quindi
**indispensabile** per usare Kite con un tracker vero. È un driver user-space
scritto in Go, con licenza MIT, che parla con il 4C via USB tramite libusb,
senza modulo del kernel né binari proprietari di Tobii, e trasmette i
campioni in **UDP, formato T4CG v2, a 90 Hz**, di default su
`127.0.0.1:5005`. È stato provato su un solo esemplare di 4C (firmware
4.7.0), su Debian 13 e Arch Linux. Installazione, permessi USB senza root e
risoluzione dei problemi sono nel
[README del driver](https://github.com/ablescia/tobii4c-linux#installazione).

Kite usa:

- lo sguardo filtrato dal firmware (stream 0x1c) e quello non filtrato
  (0x20): le saccadi si cercano su quello non filtrato, che non ha i ~30 ms
  di ritardo del filtro;
- i diametri delle pupille;
- i centri corneali, cioè la posizione degli occhi nel sistema dello
  schermo, per convertire lo sguardo in gradi di angolo visivo;
- la presenza della persona.

Il driver si avvia in due modi:

- **come servizio** utente di systemd (`tobii4c.service`, installato da
  `make install-service` del driver e attivato con
  `systemctl --user enable --now tobii4c`), che trasmette sullo stesso
  indirizzo e riapplica da solo calibrazione e geometria dello schermo
  salvate, anche dopo che il tracker è stato scollegato;
- **a mano**, in un terminale:
  `tobii4c run -headpose off -display-mm saved -calibration saved`.

Il servizio calcola anche il pitch della testa, che a Kite non serve e che
costa circa due terzi di un core. Per spegnerlo basta un override dell'unit
(`systemctl --user edit tobii4c`), con il percorso del binario che compare
nell'`ExecStart` dell'unit installata:

```ini
[Service]
ExecStart=
ExecStart=%h/.local/bin/tobii4c run -udp 127.0.0.1:5005 -display-mm saved -calibration saved -log-format json -headpose off
```

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
schermo di 700 × 390 mm visto da 62 cm, la probabilità a posteriori del tasto
guardato scende sotto 0,95 a 1,85–2,15° dal centro verso i vicini, e lì il
selettore allunga i tempi per prudenza. Lo sguardo finale si disperde già di ~0,65°
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

## Citare Kite

Se usi Kite in una ricerca, citalo come indicato in
[`CITATION.cff`](CITATION.cff) (su GitHub: «Cite this repository»). I metodi
sono descritti nella nota tecnica:

> Blescia A. (2026). *Probabilistic gaze typing with saccade-anchored adaptive
> dwell, corrective-saccade-aware selection, luminance-compensated
> pupillometry and gaze-aware correction* (versione 1.0). Zenodo.
> [doi:10.5281/zenodo.24282465](https://doi.org/10.5281/zenodo.24282465)

## Contribuire

Segnalazioni e proposte sono benvenute nelle
[issue](https://github.com/ablescia/Kite/issues) e nelle pull request. Prima
di proporre una modifica:

- `make test` (vet e test unitari, senza tracker) deve passare, e
  `make race` per le parti concorrenti;
- se la modifica tocca il selettore o i tempi, conviene misurarla con
  `kite-replay` sulle sessioni registrate;
- i nuovi file sorgente portano in testa
  `// SPDX-License-Identifier: AGPL-3.0-or-later`.

Contribuendo accetti che il tuo contributo sia distribuito con la licenza del
progetto. I problemi del tracker (USB, calibrazione, dati mancanti) vanno
segnalati al [driver](https://github.com/ablescia/tobii4c-linux/issues).

## Licenza

Copyright © 2026 Antonio Blescia. Kite è distribuito con la
[GNU AGPL v3 o successiva](LICENSE). Fanno eccezione:

- `internal/x11ui`, copiato dal driver tobii4c-linux, con licenza
  [MIT](internal/x11ui/LICENSE);
- il lessico `internal/lm/data/it_150k.txt.gz`, con licenza CC BY-SA 4.0
  ([LEGGIMI.md](internal/lm/data/LEGGIMI.md)).

I componenti di terze parti inclusi nel binario (dipendenze Go, font, lessico)
e quelli esterni usati a runtime (driver, Piper, voce) sono elencati in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md); i testi delle loro
licenze sono in [`LICENSES/`](LICENSES/).

La licenza riguarda il codice. I metodi della tastiera ibrida sono stati
pubblicati nella nota tecnica su Zenodo e non sono brevettati: restano a
disposizione di tutti.
