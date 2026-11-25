# Componenti di terze parti

Kite è distribuito con la [GNU AGPL v3 o successiva](LICENSE). Questo file
elenca i componenti di altri autori, o con un'altra licenza, che sono inclusi
nel repository o nel binario compilato, e quelli esterni che Kite usa a
runtime senza includerli.

## Inclusi in questo repository

| Componente | File | Licenza | Testo |
|---|---|---|---|
| Lessico italiano con frequenze: le prime 150.000 righe di `it_full.txt` di [FrequencyWords](https://github.com/hermitdave/FrequencyWords) di Hermit Dave, dai sottotitoli di [OpenSubtitles 2018](http://opus.nlpl.eu/OpenSubtitles2018.php) tramite OPUS | `internal/lm/data/it_150k.txt.gz`, incorporato nel binario | CC BY-SA 4.0 | [`internal/lm/data/LEGGIMI.md`](internal/lm/data/LEGGIMI.md), <https://creativecommons.org/licenses/by-sa/4.0/> |
| Interfaccia X11 a schermo intero, copiata dal driver [tobii4c-linux](https://github.com/ablescia/tobii4c-linux), Copyright (c) 2026 Antonio Blescia | `internal/x11ui/` | MIT | [`internal/x11ui/LICENSE`](internal/x11ui/LICENSE) |

## Dipendenze Go (incluse nel binario compilato)

| Modulo | Licenza | Testo |
|---|---|---|
| Libreria standard di Go, Copyright 2009 The Go Authors | BSD 3-Clause | [`LICENSES/BSD-3-Clause-Go.txt`](LICENSES/BSD-3-Clause-Go.txt) |
| [`github.com/jezek/xgb`](https://github.com/jezek/xgb), Copyright (c) 2009 The XGB Authors | BSD 3-Clause | [`LICENSES/BSD-3-Clause-XGB.txt`](LICENSES/BSD-3-Clause-XGB.txt) |
| [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image), Copyright 2009 The Go Authors | BSD 3-Clause | [`LICENSES/BSD-3-Clause-Go.txt`](LICENSES/BSD-3-Clause-Go.txt) |
| [`golang.org/x/text`](https://pkg.go.dev/golang.org/x/text), Copyright 2009 The Go Authors | BSD 3-Clause | [`LICENSES/BSD-3-Clause-Go.txt`](LICENSES/BSD-3-Clause-Go.txt) |
| Font Go Regular e Go Medium (`golang.org/x/image/font/gofont`), Copyright (c) 2016 Bigelow & Holmes Inc., incorporati nel binario | BSD 3-Clause | [`LICENSES/BSD-3-Clause-GoFonts.txt`](LICENSES/BSD-3-Clause-GoFonts.txt) |

Il codice di Kite è Go puro (si compila anche con `CGO_ENABLED=0`): il
binario si collega al più alla libc di sistema e parla con il server X11
direttamente con il protocollo, senza Xlib.

## Programmi esterni (non inclusi)

Kite li avvia o ne legge i dati, ma non li contiene né li distribuisce. Ognuno
si installa a parte, con la propria licenza.

| Componente | Ruolo | Licenza |
|---|---|---|
| [tobii4c-linux](https://github.com/ablescia/tobii4c-linux) | driver del Tobii Eye Tracker 4C, **indispensabile** con un tracker vero: Kite ne riceve i campioni via UDP e ne lancia la calibrazione (`tobii4c calibrate`) | MIT |
| [Piper](https://github.com/OHF-Voice/piper1-gpl) (`piper-tts`) | sintesi vocale locale, facoltativa: Kite la esegue come processo separato | GPL 3.0 |
| Voce italiana [`it_IT-serena-high`](https://huggingface.co/rhasspy/piper-voices/tree/main/it/it_IT/serena/high) | modello della voce per Piper, facoltativo | vedi il `MODEL_CARD` della voce (dataset [committa/serena-synthetic-it-27h](https://huggingface.co/datasets/committa/serena-synthetic-it-27h), CC BY 4.0) |

Tobii è un marchio di Tobii AB. Kite e tobii4c-linux sono progetti
indipendenti, non affiliati, sponsorizzati né approvati da Tobii.
