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
utente simulato, non ancora con chi ne ha bisogno (vedi
[Cosa è verificato](#cosa-è-verificato) e [Limiti e rischi](#limiti-e-rischi)).
Il percorso è raccontato nel diario di ricerca
[SmartChair](https://smartchair.it/ricerche/kite/).

## Che cosa serve

- Linux con X11, oppure Wayland con XWayland;
- un **Tobii Eye Tracker 4C** e il driver
  **[tobii4c-linux](https://github.com/ablescia/tobii4c-linux)**, che legge il
  tracker e manda i dati a Kite (vedi [Il driver](#il-driver-tobii4c-linux));
- Go ≥ 1.22 per compilare;
- per la voce, facoltativa: [Piper](https://github.com/OHF-Voice/piper1-gpl)
  e una voce italiana (vedi [La voce](#la-voce)).

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

# 3. la voce, una volta sola (vedi «La voce»): Piper e la voce italiana Serena
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

## Dall'architettura al codice

| Punto dell'architettura | Dove | Cosa fa esattamente |
|---|---|---|
| **1. Acquisizione** a 90 Hz, buffer di GazeX/GazeY | `internal/gazein`, `internal/engine` | Riceve i datagrammi T4CG v2 (sguardo filtrato 0x1c e non filtrato 0x20, pupille, centri corneali nel sistema dello schermo, presenza). L'engine elabora **ogni campione appena arriva** in una goroutine propria, separata dal disegno: il tempo di disegno non ritarda le decisioni. |
| **2a. Saccadi e fissazioni** | `internal/oculo` | I-VT con soglia adattiva (Nyström & Holmqvist 2010): la velocità è in **gradi di angolo visivo al secondo**, calcolata con la posizione reale degli occhi. La soglia di picco (media + 6 DS del rumore in fissazione, minimo 60°/s) si tara da sola sul tracker e sulla persona. Fine della saccade quando la velocità su 22 ms scende sotto media + 3 DS; le microsaccadi sotto 1° non spezzano la fissazione; gli ammiccamenti oltre 100 ms la chiudono. |
| **2b. Pupillometria compensata per la luce** | `internal/pupil`, `internal/app` (luminanza) | A ogni frame l'interfaccia misura la luminanza dello schermo che disegna (sRGB → lineare, griglia 48×27). La luminanza efficace pesa al 30% la zona guardata (raggio 5°) e al 70% lo schermo intero. Il modello del riflesso fotomotore, D = A + B·log₁₀(L), ha ritardo e costanti di tempo asimmetriche (la costrizione è più veloce della dilatazione) e si stima per persona, monitor e stanza con 30 s di grigi a tutto schermo. Il **residuo** (pupilla misurata − prevista dalla luce) è l'indice di carico; `z` ne è la componente fasica normalizzata (1 s contro gli ultimi 20 s). |
| **3a. Dwell adattivo** | `internal/selector` (`Hybrid`) | Il conteggio parte dalla **fine della saccade** che porta sul tasto, non da quando lo sguardo ci entra. Il tempo richiesto va da 120 ms (lettera molto probabile, r = priore × 27 ≥ 4) a 700 ms (lettera improbabile, r ≤ 0,1), lineare in log r. Si allunga se lo sguardo è ambiguo tra due tasti (posteriore < 0,95). Quattro regole, ricavate dagli errori reali (vedi [Analisi degli errori](#analisi-degli-errori-e-replay)): **un solo tasto per fissazione** (dopo uno scatto nessun altro tasto si attiva finché l'occhio non compie una saccade; lo stesso tasto si ripete solo con il tempo di ripetizione); **doppie secondo la predizione** (ripetere tenendo il tasto richiede 0,7 s se la doppia è probabile, fino a 1,5 s se no; uscendo e rientrando è immediata); **attesa della saccade correttiva** (dopo una saccade di oltre 8° che può essere caduta corta rispetto a un tasto probabile più lontano, almeno 280 ms); **prudenza dopo un errore** (la selezione successiva richiede almeno 500 ms). |
| **3b. Bersagli che si espandono in modo invisibile** | `internal/selector` (`Decoder`), `internal/lm` | Decoder bayesiano: P(tasto \| sguardo) ∝ P(tasto) · exp(−d²/2σ²), con σ = 1,6° e d la distanza dalla zona che si mira: il centro per le lettere, la linea centrale per la barra spaziatrice, che così si può guardare in tutta la sua lunghezza. Lo sguardo a più di 1° da ogni tasto (striscia del testo, zone vuote ai lati delle righe sfalsate) non attiva nulla. Un tasto probabile sposta il confine verso i vicini di σ²·ln(P_a/P_b)/d. Il **nucleo centrale** di ogni tasto (60%) resta sempre suo: guardando il centro di un tasto si ottiene quel tasto, per quanto improbabile. Il priore viene da un modello di lingua italiano (150.000 parole con le frequenze dei sottotitoli, modello per prefisso con ripiego sui bigrammi di lettere) mescolato al 10% con l'uniforme, così ogni tasto resta raggiungibile. |
| **Correttore e suggerimenti** | `internal/correct`, `internal/engine` (`editor`), `internal/lm` | Allo spazio sceglie la parola più probabile date le lettere selezionate: P(parola) dal lessico, P(selezioni \| parola) dal modello degli errori di una tastiera a sguardo (selezioni spurie, doppie saltate, tasti confusi secondo lo sguardo). Vedi [Il correttore italiano](#il-correttore-italiano). Cancella subito dopo una correzione la annulla e mette la parola nel dizionario personale. |
| **Frasi imparate** | `internal/ngram`, `internal/engine` (`phrase.go`) | Mentre si scrive propone il resto della frase tra quelle già dette, in grigio chiaro dopo il cursore. Modello a n-grammi di parole (fino a 4, interpolazione di Witten-Bell) e ricerca a fascio fino alla fine della frase. Si accetta con **una sola fissazione** di 1 s sulla frase: leggerla non basta, perché ogni saccade di almeno 1° azzera il conteggio. Vedi [Le frasi imparate](#le-frasi-imparate). |
| **Voce** | `internal/speech`, `internal/engine` (`voice.go`) | Il tasto **parla** manda il testo a Piper, sintesi neurale locale con la voce italiana Serena. Un processo con la voce già caricata aspetta il testo, così la voce parte in 0,1–0,3 s invece di 1,5. Mentre la voce parla la tastiera tace, e parla diventa **stop**. A voce finita il testo si cancella. Vedi [La voce](#la-voce). |
| **3c. Pupilla → attuazione** | `selector.HybridParams.PupilGain` | Con carico fasico z > 0 tutti i tempi crescono di 0,25·min(z, 2): al massimo +50%. È un'**ipotesi da verificare** (vedi Limiti) e si spegne con il tasto U. |
| **4. Misura** | `internal/metrics`, schermata Risultati | Caratteri/min, WPM, accuratezza, selezioni/min, ITR di Wolpaw, dwell medio, residuo pupillare, calcolati sui tempi sommati delle frasi dell'esercizio. |
| **Calibrazione dello sguardo** | `internal/driver`, `internal/app` (`gazecal.go`) | Trova il driver e chi tiene aperto il tracker (nodi USB da sysfs, descrittori in `/proc`, servizio dal cgroup), ferma e riavvia il servizio, lancia `tobii4c calibrate` sullo stesso monitor e ne legge il log JSON (esito e verifica). Vedi [Le calibrazioni](#le-calibrazioni). |

## Il menu

| Tasto | |
|---|---|
| **1** | Calibrazione dello sguardo (driver tobii4c, circa 2 minuti) |
| **2** | Calibrazione pupilla–luminanza (30 s) |
| **3** | Scrittura libera con la voce |
| **4** | Esercizio: copia alcune frasi e misura velocità e accuratezza |
| **5** | Laboratorio della pupilla |
| **R** | Risultati della sessione |
| **P, U, C** | accendono e spengono predizione, pupilla e correttore (ablazione) |
| **D, G** | diagnostica (aree di tocco, velocità, soglie, posteriore, dwell richiesto, pupilla), sguardo |
| **Esc** | esce (dalle altre schermate torna al menu) |

In fondo al menu due righe dicono quando il driver ha salvato l'ultima
calibrazione dello sguardo e se la pupilla è compensata per la luce.

### La scrittura libera (tasto 3)

È la modalità per comunicare. Basta guardare la lettera: si impara in fretta.
Le aree di tocco effettive sono disegnate in giallo, con la probabilità di ogni
tasto: è l'espansione invisibile resa visibile (D le nasconde).

- Per le **doppie** si esce dal tasto e si rientra, oppure si resta sul tasto:
  0,7 s se la doppia è probabile in quel punto della parola (pa**ss**are),
  fino a 1,5 s se non lo è.
- **Cancella** (il tasto della barra o Backspace) toglie l'ultima lettera e
  rende più prudente la selezione successiva.
- **Parla** (il tasto della barra o Invio) fa dire il testo alla voce.
- La **frase proposta** in grigio si accetta fissandola per 1 s, o con Tab.

### L'esercizio di copiatura (tasto 4)

Serve a misurare la velocità e l'accuratezza, e ad allenarsi. Sono 3 frasi
italiane di 5 parole da copiare (`-blocks` e `-words` ne cambiano il numero).
Se la pupilla non è ancora compensata per la luce, l'esercizio comincia con la
calibrazione pupilla–luminanza. Sopra c'è la frase con la parola corrente
evidenziata, sotto il testo scritto. Gli errori restano nel testo e si
correggono con **canc**, con i suggerimenti o lasciandoli al correttore. La
frase finisce con lo spazio dopo l'ultima parola. Si misura il testo finale,
come negli studi sulla scrittura. Esc interrompe l'esercizio senza registrare
la frase in corso.
Con `-task rigida` la lettera sbagliata non entra nel testo: si conta e si
seleziona quella giusta.

Alla fine i **risultati**: caratteri al minuto, WPM, testo finale corretto
(1 − CER), selezioni al minuto, ITR effettivo, dwell medio, errori commessi,
correzioni e selezioni per carattere (KSPC), in totale e frase per frase.
Nell'esercizio parla resta spento, perché cancellerebbe il testo da misurare, e
le frasi imparate non vengono proposte.

### Il laboratorio della pupilla (tasto 5)

Verifica la compensazione della pupilla sul proprio viso. Lo sfondo oscilla
lentamente tra scuro e chiaro e il grafico mostra il diametro misurato, quello
previsto dalla sola luce e il residuo. Con M si alterna il calcolo mentale
("1000 meno 7, meno 7…"): se la compensazione funziona, il residuo resta
piatto mentre la luce cambia e sale nei tratti di calcolo, evidenziati in
azzurro. L alterna la luce automatica e fissa, + e − la cambiano.

## Il correttore italiano

**Lessico.** Le 150.000 parole più frequenti dei sottotitoli italiani
(OpenSubtitles 2018 tramite FrequencyWords, licenza CC BY-SA 4.0, vedi
`internal/lm/data/LEGGIMI.md`). È la lingua parlata, la più vicina a quella
di chi comunica con una tastiera assistiva. Al caricamento la lista si
ripulisce:
- **Accenti finali obbligatori** ("perche" → "perché", "citta" → "città"),
  ma solo se la forma accentata è usata davvero ("allá" non sostituisce
  "alla").
- **Monosillabi** secondo l'ortografia: e/è, si/sì e la/là restano due
  parole; "piu" diventa "più"; "quì" diventa "qui".
- **Elisioni** come classe chiusa ("l'", "dell'", "un'"…), unite alla
  parola seguente; "po'" resta separato.
- **Rumore scartato:** abbreviazioni e apostrofi spuri.

Con 50.000 parole il correttore "correggeva" metà delle parole rare valide
in un'altra loro forma ("miserie" → "miseria"); con 150.000 le flessioni
sono quasi tutte note.

**Modello.** Allo spazio sceglie la parola che massimizza P(parola) ·
P(selezioni | parola) con un allineamento a programmazione dinamica. Ogni
lettera voluta produce una selezione, con la probabilità data dallo
sguardo. Il decoder spaziale senza predizione dice quanto lo sguardo
indicava ciascun tasto; dopo una saccade lunga si aggiunge il modello
dell'atterraggio corto, per cui una "b" raggiunta da sinistra può essere
una "n". Una selezione può essere spuria (12%) e una lettera può mancare
(2%; 25% la seconda di una doppia). Le parole si esaminano dalla più
frequente, e un limite rapido sulle lettere in comune scarta quasi tutte le
altre: circa 8 ms per parola.

**Prudenza.** Cambiare una parola giusta è peggio che lasciare un refuso,
quindi:
- **Parole note:** una parola del lessico con almeno ~240 occorrenze non
  viene sostituita, al più prende accenti e apostrofi.
- **Parole fuori lessico plausibili:** se nessun trigramma di lettere è
  improbabile in italiano (modello a trigrammi sui tipi del lessico), la
  parola non viene toccata. Le selezioni spurie creano sequenze che non
  esistono ("sab**ng**ue", "**dp**ensiero"); nomi e parole rare no.
- **Eccezione:** le doppie saltate ("pasare" → "passare") si correggono
  anche quando la parola sembra plausibile.
- **Soglia:** serve una probabilità di almeno 0,5.

Quello che non si corregge da solo resta nella barra dei suggerimenti.
Cancella subito dopo una correzione la annulla, e la parola entra nel
dizionario personale.

**Sui dati reali** (`./bin/kite-replay -correttore`): le parole copiate nelle
sessioni registrate al banco, ricostruite con tutte le lettere selezionate come
sarebbero entrate nel testo, sono 40, di cui 19 con errori. Il correttore ne
sistema 10 allo spazio, altre 5 compaiono tra i tre suggerimenti, 4 restano
sbagliate (errori pesanti come "vecrerccaare"). Non guasta nessuna delle 21
parole giuste. Le parole giuste passano da 21 a 31 su 40, a 36 con
un'occhiata alla barra. Su mille parole valide prese dalle 30.000 più
frequenti e scritte senza errori, nessuna viene cambiata. Su un elenco di
nomi, cognomi e termini (logopedista, fisiatra…) fuori lessico, nessuno
viene cambiato.

Su questi dati il modello con lo sguardo non fa meglio di quello con le
sole lettere. Gli errori registrati sono occhiate di ricerca con lo sguardo
ben piantato sul tasto sbagliato, quindi lettere in più, non tasti vicini
confusi. Lo sguardo può fare la differenza sulle sostituzioni tra tasti
vicini, che nel compito "rigida" non entravano nel testo: lo diranno le
sessioni con il compito "testo".

## La voce

Chi usa una tastiera a sguardo per comunicare scrive un messaggio e poi lo
fa dire al computer. Qui lo fa il tasto **parla**, a destra di cancella nella
barra sopra la tastiera, nella scrittura libera (tasto 3).

**Come funziona.**
1. Si scrive il messaggio. Parla è grigio finché il testo è vuoto.
2. Si guarda **parla** (come cancella: 700 ms, solo guardandolo dentro).
   L'ultima parola si chiude come con lo spazio, quindi il correttore la
   sistema prima che la voce la dica.
3. **Mentre la voce parla la tastiera tace.** Il testo diventa azzurro e le
   lettere grigie: chi ascolta, o guarda la persona con cui parla, non scrive
   per sbaglio. Parla diventa **stop**, ma si accende solo dopo che lo sguardo
   ne è uscito. Chi ha appena scelto parla e continua a guardarlo non ferma
   la voce. Con il dwell di ripetizione da 1,5 s succederebbe.
4. **A voce finita il testo si cancella.** Se la voce viene fermata, o non
   riesce a parlare (l'errore appare in rosso), il testo resta.
5. **Cancella subito dopo** ripristina il testo detto, per correggerlo o
   ridirlo, come dopo una correzione automatica. Una lettera comincia
   invece un messaggio nuovo.

Sulla tastiera fisica Invio fa lo stesso (parla o stop). Esc o un cambio di
modalità fermano la voce. Nell'esercizio parla resta spento, perché
cancellerebbe il testo da misurare.

**Perché Piper e Serena.** [Piper](https://github.com/OHF-Voice/piper1-gpl) è
sintesi neurale (VITS) che gira in locale sulla CPU, senza rete né GPU. Per
l'italiano ha quattro voci. `it_IT-serena-high` è la più naturale: qualità
alta, addestrata da zero su 27 ore di parlato sintetico (dataset
`committa/serena-synthetic-it-27h`, CC BY 4.0). La
seconda scelta è `it_IT-paola-medium`, più leggera, con un timbro diverso.
Le altre sono `serena-medium` e `riccardo-x_low`. Senza `-voice` si usa la
prima installata in quest'ordine. Le voci si cercano in
`~/.local/share/piper/voices` e in `/usr/share/piper-voices`, anche nelle
sottocartelle. Piper (GPL-3.0) gira come programma separato: Kite gli
passa il testo e ne legge l'audio.

**Tempi misurati** su un Ryzen 9 5950X, per una frase di circa 3 s, dal testo al primo audio:

| | Serena (high) | Paola (medium) |
|---|---|---|
| voce da caricare (avvio a freddo) | 1,6 s | 1,2 s |
| voce già caricata | 0,30 s | 0,08 s |
| sintesi rispetto alla durata dell'audio | 12% | 6% |

Per questo **un processo di Piper resta sempre pronto**, con la voce in
memoria, in attesa del testo. Quando la voce parla, quel processo riceve il
testo e se ne prepara un altro mentre l'audio suona. Il nuovo processo si
avvia a sintesi finita, così non rallenta quella in corso. «Grazie» con Serena,
dal comando alla fine dell'audio: 0,69 s. L'audio, PCM a 22 050 Hz,
va a PipeWire con `pw-play` (o a `paplay` o `aplay`). Il testo si cancella
quando il riproduttore ha finito di suonare, non quando ha ricevuto l'audio.

**Installazione.** Vedi l'[avvio rapido](#avvio-rapido): Piper con `uv tool
install piper-tts` (in alternativa i pacchetti AUR `piper-tts-bin` o
`piper-tts`) e la voce da [rhasspy/piper-voices](https://huggingface.co/rhasspy/piper-voices).
`./bin/kite -say "testo"` prova la voce senza aprire la finestra. Se
Piper o la voce mancano, Kite funziona lo stesso: la scrittura libera
dice perché la voce non c'è e parla resta spento.

## Le frasi imparate

Chi comunica con una tastiera a sguardo ripete spesso gli stessi messaggi:
«ho sete», «mi giri sul fianco», «chiamate mia figlia». Nella scrittura
libera Kite li impara e, dopo poche lettere, propone il resto della frase
in **grigio chiaro** dopo il cursore. Scriverla tutta costerebbe una
selezione per lettera; accettarla ne costa una.

**Come funziona.**
1. Si scrive l'inizio del messaggio. Appena una frase imparata è abbastanza
   probabile, il resto compare in grigio: «ho s|ete vorrei dell'acqua». Sul
   testo vuoto non compare nulla, perché si accetterebbe guardando nel vuoto
   mentre si pensa a cosa scrivere.
2. Per accettarla si **fissa la frase grigia**. Una linea azzurra sotto la
   frase mostra l'avanzamento. Dopo 1 s la frase entra nel testo, con gli
   accenti e le elisioni con cui è stata imparata. Tab sulla tastiera fisica
   la accetta subito.
3. **Leggerla non la accetta.** Il conteggio vale per una sola fissazione.
   Chi legge salta di parola in parola con saccadi di qualche grado, e ogni
   saccade lo azzera. Le microsaccadi (sotto 1°) e gli ammiccamenti fino a
   300 ms non lo azzerano. L'area che accetta è la frase con 0,8° di margine,
   e non scende mai sui tasti.
4. **Cancella subito dopo** annulla l'accettazione. Torna il testo di prima,
   e la stessa frase non viene riproposta finché il testo non cambia.
5. Si può anche ignorarla e continuare a scrivere. La proposta si aggiorna a
   ogni lettera e scompare se nessuna frase è abbastanza probabile.

**Come impara.** Un messaggio si impara quando la voce lo ha detto per
intero. Una frase accettata e poi detta si rinforza. Una frase cambiata prima
di dirla si impara nella versione detta. Una voce fermata con stop, o che non
riesce a parlare, non insegna nulla. Senza voce, quindi, non si impara. Le
frasi si salvano in `~/.config/kite/frasi.txt`, una per riga. Le righe
ripetute contano come usi, e il file si può modificare a mano. Si parte
senza frasi. Con l'utente simulato il file non si legge né si scrive.
Nell'esercizio le frasi non vengono proposte.

**Il modello** (`internal/ngram`). Conta gli n-grammi di parole delle frasi
imparate, fino a quattro parole (tre di contesto), con l'inizio e la fine
della frase. La probabilità della parola successiva mescola gli ordini con il
metodo di Witten-Bell: un contesto mai visto ricade su quello più corto, e una
parte della probabilità resta alle parole mai usate. Con poche frasi il
modello sa di sapere poco e propone meno. La parola in corso vincola la prima
parola della proposta. Le parole mai viste che iniziano con le stesse lettere
pesano quanto dice il lessico italiano. Una ricerca a fascio (8 ipotesi)
trova il completamento più probabile fino alla fine della frase. Lo propone
se la probabilità dell'intero completamento è almeno 0,3 (`-phrase-min`).

Con 16 frasi imparate, tra cui «ho sete» tre volte e «mi fa male la testa»
due volte contro una di «mi fa male la gamba»:

| Scritto | Proposta | Probabilità |
|---|---|---|
| «h» | «o sete» | 0,36 |
| «ho s» | «ete» | 0,97 |
| «mi fa male la » | «testa» | 0,58 |
| «per» | « favore chiamate mia figlia» | 0,45 |

Se testa, gamba, schiena e braccio sono stati detti una volta ciascuno, dopo
«mi fa male la » non compare nulla, finché una lettera non decide. Il
contesto sono le ultime tre parole, quindi la frase si riconosce anche dentro
un messaggio iniziato in un altro modo («allora per favore chiamate m» →
«ia figlia»). Con 3000 frasi una proposta costa circa 5 µs. Si ricalcola a
ogni lettera, dentro l'elaborazione dei campioni, senza rallentarla.

**Perché non una libreria esterna.** Le librerie Go chiamate go-ngram
lavorano sugli n-grammi di *caratteri*: `Lazin/go-ngram` è un indice per la
ricerca approssimata di stringhe, `euskadi31/go-ngram` un tokenizer. Nessuna
delle due predice parole o frasi. Il modello è scritto in Go puro, come il
resto del progetto, senza dipendenze nuove.

## I dati della sessione

Ogni avvio è una sessione, in `~/.local/share/kite/sessions/<data-ora>/` (o in
`-out`):

| File | Contenuto |
|---|---|
| `samples.csv` | un campione per riga (90 Hz): sguardo filtrato e non filtrato, pupille, posizione degli occhi, fase oculare, velocità e soglie, fissazione, tasto candidato, avanzamento, dwell richiesto, pupilla misurata, prevista, residuo, z, luminanza |
| `events.jsonl` | saccadi (durata, ampiezza, picco di velocità), selezioni (tasto, tipo, testo dopo la selezione, atteso, corretto, dwell, posteriore, priore, probabilità di mira dopo una saccade lunga, latenza di decisione), decisioni del correttore (parola selezionata, scritta, probabilità, alternative), parole aggiunte al dizionario personale, voce (`speech_start` con il testo detto, `speech_end` con esito e durata), frasi proposte (`phrase_shown`: testo, frase, probabilità), accettate (`phrase_accept`: con lo sguardo o Tab, dopo quanta fissazione) e imparate (`phrase_learn`), inizio e fine delle frasi dell'esercizio e delle calibrazioni (`gaze_calib_end` con esito, verifica, servizi fermati e riavviati) |
| `block-NN.json` | una frase dell'esercizio con tutte le selezioni e il risultato |
| `summary.json` | risultati per frase e in totale, opzioni |
| `session.json`, `pupil-calibration.json` | parametri della sessione (anche area della tastiera, compito, modello di lingua, correttore, frasi imparate) e modello della pupilla |
| `gaze-calibration-<ora>.log` | output del driver durante la calibrazione dello sguardo |

Nei log le frasi dell'esercizio hanno la condizione `"B"`, il selettore
ibrido, come nelle sessioni del banco di prova da cui Kite nasce: il replay le
legge allo stesso modo.

Il dizionario personale è in `~/.config/kite/parole.txt`, una parola per riga.
Ci finiscono le parole di cui si annulla la correzione, e si può modificare a
mano; con l'utente simulato non si legge né si scrive. Lo stesso vale per le
frasi imparate, in `~/.config/kite/frasi.txt` (vedi
[Le frasi imparate](#le-frasi-imparate)). Al primo avvio, se esistono i file
del banco di prova in `~/.config/tobii4c-hybridbci/` e Kite non ha ancora i
suoi, Kite li copia (gli originali restano dove sono).

## Parametri

| Flag | Default | Significato |
|---|---|---|
| `-tmin`, `-tmax` | 120ms, 700ms | tempo minimo (lettera probabile) e massimo (improbabile) dopo la saccade |
| `-repeat`, `-repeat-min` | 1,5s, 700ms | ripetere la lettera tenendo il tasto, se la doppia è improbabile e se è probabile (`-repeat-min 0`: sempre `-repeat`) |
| `-land` | 280ms | attesa minima dopo un atterraggio forse corto (0 = spenta) |
| `-caution` | 500ms | tempo minimo della selezione dopo un errore (0 = spento) |
| `-sigma` | 1,6 | dispersione dello sguardo attorno al centro del tasto, in gradi (più alta = espansione più ampia) |
| `-core` | 0,6 | frazione centrale di ogni tasto che non si cede ai vicini |
| `-eps` | 0,1 | quota uniforme nel priore |
| `-pupil-gain` | 0,25 | allungamento dei tempi per unità di carico pupillare |
| `-no-prediction`, `-no-pupil`, `-no-correct` | — | senza predizione, senza pupilla o senza correttore e suggerimenti (ablazioni) |
| `-no-phrases` | — | scrittura libera senza frasi proposte, e senza impararne |
| `-phrase-dwell` | 1s | fissazione sulla frase grigia che la accetta (almeno 300ms) |
| `-phrase-min` | 0,3 | probabilità minima del completamento per proporlo |
| `-blocks`, `-words` | 3, 5 | frasi dell'esercizio e parole per frase |
| `-task` | testo | compito dell'esercizio: `testo` (gli errori entrano nel testo e si correggono) o `rigida` (la lettera sbagliata non entra) |
| `-lum-fovea` | 0,3 | peso della zona guardata nella luminanza efficace |
| `-layout` | qwerty | disposizione `qwerty` (righe sfalsate, barra spaziatrice larga 5 tasti) o `abc` (griglia alfabetica 3×9) |
| `-display-mm`, `-monitor` | da `~/.config/tobii4c/display.json` | geometria dello schermo e monitor |
| `-tobii4c` | cercato | eseguibile del driver per la calibrazione dello sguardo |
| `-voice` | la più naturale installata | voce di Piper: nome (`it_IT-paola-medium`) o file `.onnx`; `no` = senza voce |
| `-voice-speed` | 1 | velocità della voce (0,8 = più lenta) |
| `-piper` | cercato | eseguibile di Piper |
| `-say` | — | dice il testo con la voce ed esce (prova dell'audio, senza finestra) |
| `-udp` | 127.0.0.1:5005 | dove ricevere i campioni del driver |
| `-simulate`, `-headless`, `-auto`, `-shots`, `-script` | — | utente simulato, nessuna finestra, esercizio automatico, screenshot, tasti scriptati (test) |
| `-exercise`, `-debug`, `-seed`, `-out` | — | parte con l'esercizio, diagnostica accesa, seme dei testi, cartella della sessione |

## Analisi degli errori e replay

Kite nasce da un banco di prova che confrontava, sullo stesso compito, la
tastiera ibrida con una tastiera a dwell fisso di 1 s. Nella prima sessione
reale con la QWERTY (2026-11-06) la tastiera ibrida andava a 56,9 car/min
contro i 35,6 del dwell fisso, ma con il **19% di selezioni sbagliate** (23
su 123). Ricostruiti dai campioni, gli errori avevano quattro cause:

| Causa | Errori | Meccanismo |
|---|---|---|
| Occhiate di ricerca | 10 | Lo sguardo cerca la lettera e una fissazione di 250–480 ms su un tasto mediamente probabile scatta; 5 erano a catena dopo un altro errore. |
| Atterraggio corto | 6 | Le saccadi lunghe coprono il 93–95% della distanza (mediana): circa metà dei primi atterraggi cade sul tasto vicino, quasi sempre dal lato da cui arriva lo sguardo, e ci resta ~200 ms prima della saccade correttiva. Il selettore scattava prima. |
| Lettere ripetute o scivolate | 5 | Restando sul tasto oltre 700 ms partiva una doppia (4); in un caso lo sguardo è scivolato sul tasto accanto nella stessa fissazione. |
| Espansione predittiva | 2 | Sguardo sul bordo o dentro il tasto giusto, assegnato al vicino più probabile. |

Le regole del punto 3a nascono da qui. Per misurarle senza chiedere ogni
volta una sessione nuova, **`kite-replay`** rigioca le sessioni registrate
(`samples.csv`, `events.jsonl`, `session.json`) attraverso il rilevatore di
saccadi e il selettore, con parametri a scelta:

```bash
make replay                        # registrato contro attuale, tutte le sessioni
./bin/kite-replay -suite           # contributo di ogni regola e varianti dei valori
./bin/kite-replay -v <sessione>    # elenco degli errori e delle lettere in ritardo
./bin/kite-replay -correttore -v   # il correttore sulle parole copiate
```

Senza argomenti rigioca le sessioni del banco di prova
(`~/.local/share/tobii4c-hybridbci/sessions`), se ci sono, e quelle di Kite
(`~/.local/share/kite/sessions`); `-sessions` indica altre cartelle. Nelle
sessioni del banco i blocchi "A" sono scritti con il dwell fisso: il replay
ne rigioca lo sguardo con il selettore ibrido, e dice come si comporterebbe
con lo sguardo di chi non lo conosce.

Con i parametri registrati il replay riproduce il log: su una sessione
simulata selezione per selezione; su quelle reali 197 su 198, a meno degli
arrotondamenti del CSV.

Il replay però non è un esperimento: la persona aveva davanti il sistema di
allora. Per restare in sincronia con lei segue i tasti che ha scritto senza
sbagliare. Una lettera che il selettore nuovo non attiva prima che la
persona lasci il tasto conta come **in ritardo**, del tempo che mancava,
perché dal vivo lei avrebbe continuato a guardarla. Nel ritardo medio non
contano le lettere anticipate di oltre un secondo: lì il replay ha perso la
sincronia con lei.

Risultati sui blocchi della tastiera ibrida delle tre sessioni reali (198
selezioni):

| Variante | Errori | Accuratezza | Ritardo per lettera rispetto alla prima versione |
|---|---|---|---|
| prima versione | 37 | 81,4% | — |
| + una selezione per fissazione | 35 | 82,1% | 0 ms |
| + attesa della correttiva | 34 | 82,5% | +2 ms |
| + doppie secondo la predizione | 25 | 86,5% | +2 ms |
| + prudenza dopo un errore | 19 | 89,4% | +24 ms |
| + lessico italiano grande (attuale) | 20 | 88,9% | +35 ms |

Sono stati provati e scartati, perché peggiorano o costano più di quanto
rendono:
- **σ più stretto (0,8°).** Lo sguardo finale si disperde di ~0,65° attorno
  al centro, ma con σ = 1,6° lo sguardo vicino al bordo allunga il tempo
  richiesto, e questa prudenza sui bordi evita errori.
- **Nucleo più ampio.**
- **Attesa fissa dopo ogni saccade lunga, senza modello di atterraggio.**
  Stessi errori, ritardo triplo.
- **`-tmin` a 150–200 ms.**
- **Ricalibrazione implicita dello scarto sistematico** (~0,45° a destra e
  in alto). Nelle selezioni rapide la stima è sporcata dagli atterraggi
  corti.

La pupilla non mostra risposta agli errori (±0,02 mm). Gli errori che
restano sono soprattutto occhiate di ricerca, ed è lì che interviene il
correttore.

Il primo blocco con queste regole registrato dal vivo (sessione delle 12:45)
è andato a 58,9 car/min con accuratezza 89,2%, contro l'81% della sessione
precedente. Il passaggio al lessico grande (vedi sopra) costa sui blocchi
registrati un errore e ~11 ms per lettera rispetto al modello da 544 parole.
È atteso: quel modello conosceva solo le parole dei testi da copiare, quindi
era più favorevole del dovuto.

## Cosa è verificato

**Test automatici** (`make test`, `make race`):
- `oculo`: una saccade di 10° con profilo minimum-jerk viene rilevata una
  volta sola, con l'inizio della fissazione entro 23 ms dall'atterraggio reale
  e la stabilità entro 60 ms. Il rumore da 0,15° a 0,5° per 10 s non produce
  false saccadi. Un ammiccamento apre una fissazione nuova senza saccade, una
  microsaccade di 0,6° no.
- `pupil`: su dati sintetici il fit ritrova A, B e il ritardo esatti
  (R² 0,997). Una pupilla che non risponde alla luce viene rifiutata. Con la
  luce che sposta la pupilla di 1,7 mm il residuo resta entro 0,06 mm, e un
  carico di 0,25 mm dà z = 5.
- `selector`: disposizione QWERTY (sfalsamento delle righe, barra
  spaziatrice centrata); la barra si seleziona guardandola in qualunque
  punto, mentre col modello "mira al centro" i suoi estremi finivano sulla X
  e sulla N con certezza; le zone vuote ai lati delle righe non attivano
  nessun tasto; espansione verso il tasto probabile, ancoraggio del nucleo,
  nessun tasto fuori dalla tastiera, dwell adattivo
  (120 / 338 / 700 ms per r = 4 / 1 / 0,1), doppie, nessuna selezione durante
  le saccadi. Regole: lo sguardo che scivola sul vicino nella stessa
  fissazione non scrive (con la prima versione sì); doppia in 0,7 s se
  probabile, 1,5 s se no, immediata uscendo e rientrando; partendo dalla Q e
  atterrando sulla I, con la O probabile, la mira sulla I vale 0,20 e la I
  aspetta 280 ms invece di 182, mentre atterrando sulla O non si aspetta;
  dopo un errore una sola selezione prudente.
- `replay`: una sessione registrata dal motore con l'utente simulato e
  rigiocata con i suoi parametri dà le stesse selezioni, errori compresi. Un
  blocco "A" come quelli del banco di prova si rigioca con il selettore
  ibrido senza toccare la fedeltà degli altri.
- `lm`: il modello di base riproduce esattamente le probabilità della
  prima versione (le sessioni registrate si rigiocano con quello); il
  lessico normalizza accenti, monosillabi ed elisioni ("perché", "e/è",
  "alla" e non "allá", "l'"); il dizionario personale non tocca il lessico
  condiviso.
- `correct`: correzione di selezioni spurie, atterraggi corti, doppie
  saltate, accenti ed elisioni; nomi e parole plausibili fuori lessico
  restano; i refusi "plausibili" ("fiorse") non si correggono da soli ma
  compaiono tra i suggerimenti; mille parole valide scritte giuste restano
  intatte; meno di 10 ms per parola.
- `engine` (editor): correzione allo spazio, annullamento con cancella e
  parola nel dizionario personale, elisioni ("l acqua" → "l'acqua"),
  suggerimenti accesi solo se esistono, rientro nella parola precedente.
- `engine` (esercizio), con l'utente simulato (rumore e filtro tarati sul 4C
  misurato): quattro frasi nel compito "rigida" a circa 94 car/min con il 92%
  di selezioni corrette e un dwell medio di 224 ms; quattro nel compito
  "testo", dove l'utente simulato corregge con cancella, a circa 84 car/min con
  il testo finale identico a quello da copiare. In entrambi i casi più di
  60 car/min, il limite di qualunque dwell fisso di 1 s.
- `selector`: barra di modifica: si attiva solo guardandola dentro, lo
  sguardo appena sopra (sul testo) non attiva nulla, i suggerimenti vuoti
  non esistono, le lettere hanno gli stessi tempi con o senza barra.
- `metrics`, `gazein`: distribuzioni normalizzate e predizioni attese,
  ITR di Wolpaw, aggregati, parsing del pacchetto T4CG v2 con gli offset del
  driver.
- `app`: ogni schermata disegnata in headless e una frase dell'esercizio
  portata fino al risultato dall'utente simulato, senza data race.
- `driver` e `app` (calibrazione dello sguardo), con un driver finto: gli
  argomenti passati a `tobii4c calibrate` (log JSON, monitor), l'esito letto
  dal log (salvata, interrotta con la calibrazione precedente rimessa, errore
  in chiaro), la verifica (una ricalibrazione azzera quella vecchia), l'output
  salvato nella sessione; con il tracker tenuto da un driver avviato a mano la
  calibrazione non parte, e parte appena il tracker è libero; la finestra di
  Kite si nasconde e torna; uno schermo misurato diverso chiede il riavvio,
  ma non con `-display-mm`.
- `speech`, con un Piper e un riproduttore finti: il testo arriva al processo
  già pronto, l'audio arriva intatto al riproduttore, `Say` torna solo a
  riproduzione finita, dopo ogni frase si prepara un processo nuovo, stop
  interrompe subito, un errore di Piper arriva con la sua ultima riga di
  stderr. Ricerca delle voci con la preferenza per la più naturale. Con
  Piper e Serena installati, una sintesi vera.
- `engine` (voce): parla dice il testo con l'ultima parola corretta
  ("sabngue" → "sangue"), la tastiera tace mentre parla, stop si accende solo
  con lo sguardo fuori dal tasto, a voce finita il testo si cancella e
  cancella lo ripristina; stop, un errore della voce o il ritorno al menu
  lasciano il testo; nell'esercizio parla resta spento. Lo stesso **con lo
  sguardo** (campioni sintetici dentro il selettore ibrido): "ciao" scritto,
  parla, 2,5 s ancora su parla senza fermare la voce, una lettera guardata
  che non entra, stop, di nuovo parla, testo cancellato. Senza la regola
  dello sguardo fuori dal tasto, questo test fallisce.
- `ngram`: parole e forme (elisioni staccate dalla parola dopo, «po'» no,
  punteggiatura scartata); completamento a ogni punto della frase, con
  accenti ed elisioni come imparati («perch» → «é non vieni», «vorrei del» →
  «l'acqua fresca»). La frase più usata vince, ma la lettera scritta conta
  più della frequenza. Con continuazioni alla pari non c'è proposta. Il
  contesto di tre parole funziona dentro un messaggio diverso. Una frase
  sola non dà certezza.
- `engine` (frasi), con campioni sintetici dentro il selettore ibrido: 1,2 s
  di fissazione sulla frase grigia la accettano, e a metà l'avanzamento è
  tra 0,3 e 1. Cancella annulla, e la stessa frase torna solo dopo una
  lettera. 24 fissazioni di lettura da 300 ms non la accettano: senza
  l'azzeramento alla saccade questo test fallisce. Una fissazione con un
  ammiccamento di 150 ms la accetta. Non la accettano lo sguardo fuori dalla
  frase, l'area non ancora comunicata, l'area di una proposta vecchia. Tab ed
  elisioni; nessuna proposta nell'esercizio né sul testo vuoto. Un messaggio
  detto per intero si impara e torna come proposta; uno fermato con stop no.
- `app`, la catena intera: lo sguardo scrive «ho s» guardando i tasti,
  l'interfaccia disegna la frase in grigio e ne comunica l'area, e fissarla
  la scrive.
- `selector`, `replay`: la barra con parla ha 5 tasti. Quella delle sessioni
  registrate prima della voce resta identica (4 tasti), e il replay delle
  sessioni reali dà lo stesso risultato di prima, riga per riga.

**Sul 4C reale** (2026-11-06, persona a ~70 cm, dati del driver in UDP): 90 Hz,
sguardo valido nell'88% dei campioni e pupilla nell'83%. La precisione
(RMS campione-campione in fissazione) è 0,27° sullo sguardo non filtrato e
0,19° su quello filtrato, che è in ritardo di **~30 ms**. Le fissazioni
spontanee durante il lavoro durano 133–344 ms (quartili, mediana 211 ms). La
soglia adattiva delle saccadi si è assestata al minimo (60°/s) contro picchi
di oltre 300°/s.

**Non ancora verificato**:
- **con le persone a cui Kite è destinato.** Le misure vengono da sessioni al
  banco con una persona e dall'utente simulato. Il simulatore prova che la
  catena misura correttamente, non quanto è veloce una persona: le sue
  latenze, la ricerca visiva e gli errori di atterraggio sono ipotesi;
- **la calibrazione dello sguardo lanciata da Kite sul 4C reale**, con la
  finestra X11 nascosta e ripresa. Il flusso è provato con un driver finto e
  un display headless; il riconoscimento del driver (eseguibile, tracker
  collegato, servizio, processi che lo tengono) è provato in sola lettura sul
  sistema reale.

## Limiti e rischi

- **Midas touch.** È il rischio principale. 120 ms è meno della durata della
  maggior parte delle fissazioni spontanee misurate (mediana 211 ms):
  guardare un tasto probabile *senza volerlo scrivere* lo attiva. La
  protezione viene dal modello di lingua (le lettere improbabili richiedono
  fino a 700 ms), dal compito (si guarda una lettera per scriverla) e dalle
  regole del punto 3a. Le occhiate di ricerca restano la prima causa di
  errore; alzare `-tmin` le riduce, ma sui dati reali costa più tempo di
  quanto ne fa risparmiare (vedi il replay).
- **La pupilla è lenta.** La dilatazione da carico arriva dopo 0,5–1,5 s,
  quindi non può decidere una singola selezione da 120–300 ms. Qui modula i
  tempi su scala di secondi, ed è un'ipotesi: va confrontata con U spento.
  L'uso più solido è come **misura** del carico, che Kite registra.
- **Luminanza relativa.** La luce si calcola dai pixel disegnati, non in
  cd/m². Se cambiano la luminosità del monitor o la luce della stanza va
  rifatta la calibrazione. L'errore di scorcio della pupilla (la dimensione
  apparente cambia con la direzione dello sguardo) non è compensato; lo
  sguardo è comunque registrato per correggerlo offline. La qualità della
  pupillometria del 4C non è documentata da Tobii: il laboratorio serve a
  verificarla.
- **Correttore senza contesto.** Sceglie ogni parola da sola: non usa le
  parole precedenti, quindi "c'e" non diventa "c'è" (la "è" resta tra i
  suggerimenti). Un modello di coppie di parole lo migliorerebbe, ma i dati
  di frequenza disponibili sono di parole singole. Le flessioni oltre le
  150.000 parole e i nomi che "non sembrano italiani" possono essere
  corretti la prima volta; cancella annulla e la parola si impara.
- **Replay del compito "testo".** È approssimato: i suggerimenti mostrati
  allora non si ricostruiscono e lo sguardo registrato dipende da quello che
  la persona vedeva.
- **Statistica.** Poche frasi su una persona indicano una tendenza, non
  dimostrano un effetto. Servono più sessioni (l'allenamento conta) e più
  persone, idealmente gli utenti finali. L'ITR di Wolpaw assume errori
  uniformi e indipendenti.
- **Voce.** La tastiera non ha punteggiatura, quindi il messaggio arriva a
  Piper come una sola frase, con il punto finale aggiunto. La voce comincia
  solo quando tutta la frase è sintetizzata: un messaggio di 10 s parte dopo
  circa 1,2 s con Serena e 0,6 s con Paola. Le lettere sono minuscole e i
  nomi propri si leggono come parole comuni. Il primo processo carica la voce
  all'avvio del programma (1,6 s di CPU): un «parla» nei primi istanti
  aspetta il caricamento. Il nome `piper` nel PATH è anche quello del
  configuratore dei mouse (libratbag): si cercano prima `piper-tts` e
  `~/.local/bin/piper`, altrimenti serve `-piper`.
- **Frasi.** La proposta arriva sempre fino alla fine della frase. Quando le
  frasi imparate si dividono alla pari (stesso inizio, finali diversi), non
  compare nulla finché una lettera non decide. Le lettere della parola in
  corso devono essere giuste: un refuso la spegne fino allo spazio, quando
  il correttore sistema la parola. Le frasi non si dimenticano da sole: una
  detta molte volte un anno fa pesa come una di ieri (si toglie da
  `frasi.txt`). 1 s di fissazione è un compromesso tra accettazioni
  involontarie e fatica, da verificare con una persona (`-phrase-dwell`).
- **Calibrazione dello sguardo.** Kite la affida al driver: senza `tobii4c`
  installato non si fa. Un driver avviato a mano va chiuso e poi riavviato a
  mano; un `tobii4c.service` di sistema (non utente) Kite non può fermarlo.
  Mentre si calibra, i dati non arrivano.
- Solo X11 o XWayland, come l'app di calibrazione del driver.

## Struttura

| Pacchetto | Ruolo |
|---|---|
| `cmd/kite` | CLI: flag, geometria, sorgente (UDP o simulazione), display |
| `cmd/replay` (`kite-replay`), `internal/replay` | rigioca le sessioni registrate con parametri a scelta |
| `internal/gazein` | ricevitore UDP T4CG v2, geometria schermo → gradi |
| `internal/oculo` | saccadi e fissazioni (I-VT adattivo) |
| `internal/pupil` | modello pupilla–luminanza, fit, residuo e indice di carico |
| `internal/lm` | lessico italiano (150.000 parole, `data/`), modello per prefisso e bigrammi, dizionario personale |
| `internal/correct` | correttore italiano a canale rumoroso e completamenti |
| `internal/ngram` | frasi imparate: n-grammi di parole (Witten-Bell) e ricerca a fascio del completamento |
| `internal/speech` | voce: Piper con un processo sempre pronto, riproduzione con PipeWire, PulseAudio o ALSA |
| `internal/selector` | tastiera, decoder bayesiano, selettore ibrido |
| `internal/metrics` | metriche delle frasi dell'esercizio, aggregati, ITR |
| `internal/engine` | elaborazione per campione, esercizio, testo con correttore (`editor`), voce (`voice.go`), frasi proposte (`phrase.go`), log della sessione |
| `internal/driver` | calibrazione dello sguardo con il driver tobii4c: chi tiene il tracker, servizio, `tobii4c calibrate` |
| `internal/sim` | utente simulato (oculomotore e pupilla) |
| `internal/app` | interfaccia: menu, calibrazioni, scrittura libera con la voce e le frasi, esercizio, risultati, laboratorio |
| `internal/x11ui` | finestra X11 in Go puro, copiata dal driver (MIT) |

## I metodi

I metodi della tastiera ibrida, con le loro varianti, sono descritti in una
divulgazione tecnica pubblicata su Zenodo:

> Blescia A. (2026). *Probabilistic gaze typing with saccade-anchored
> adaptive dwell, corrective-saccade-aware selection, luminance-compensated
> pupillometry and gaze-aware correction* (versione 1.0). Zenodo.
> [doi:10.5281/zenodo.24282465](https://doi.org/10.5281/zenodo.24282465)

## Licenza

Kite è software libero: lo si può ridistribuire e modificare secondo i
termini della **GNU Affero General Public License**, versione 3 o (a scelta)
successiva. Vedi [LICENSE](LICENSE). Ogni file sorgente lo dichiara con
`SPDX-License-Identifier: AGPL-3.0-or-later`.

Fanno eccezione:
- `internal/x11ui`, copiato dal driver tobii4c-linux: licenza MIT
  ([internal/x11ui/LICENSE](internal/x11ui/LICENSE));
- `internal/lm/data/it_150k.txt.gz`, il lessico: Creative Commons
  Attribution-ShareAlike 4.0, vedi
  [internal/lm/data/LEGGIMI.md](internal/lm/data/LEGGIMI.md).

Piper (GPL-3.0) e le voci non fanno parte di Kite: si installano a parte e
girano come programma separato.
