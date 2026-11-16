# Lessico italiano con frequenze

`it_150k.txt.gz` sono le 150.000 parole più frequenti nei sottotitoli
italiani di OpenSubtitles 2018, con il numero di occorrenze (da 7,4 milioni
per "e" a 13), cioè le prime 150.000 righe di `it_full.txt` del progetto
FrequencyWords di Hermit Dave, compresse con gzip:

- sorgente: <https://github.com/hermitdave/FrequencyWords>
  (`content/2018/it/it_full.txt`, sha256
  b23b0c6a3f59c1da1c7caa667b9df5699e95323fee20a05f343c7d7dae73c4be);
- dati originali: OpenSubtitles 2018 tramite OPUS,
  <http://opus.nlpl.eu/OpenSubtitles2018.php>;
- licenza del file: **Creative Commons Attribution-ShareAlike 4.0**
  (CC BY-SA 4.0), <https://creativecommons.org/licenses/by-sa/4.0/>.

La licenza vale per questo file e per le sue modifiche, non per il codice
del progetto. La pulizia (accenti, apostrofi, abbreviazioni) avviene al
caricamento in `internal/lm/italian.go`.

Perché 150.000 e non 50.000 parole: l'italiano ha molte forme flesse, e
con 50.000 il correttore "correggeva" metà delle parole rare valide in
un'altra forma della stessa parola ("miserie" → "miseria"). Oltre le
150.000 (meno di 13 occorrenze) prevalgono i refusi dei sottotitoli.
