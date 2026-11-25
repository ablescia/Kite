// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package lm

// lexicon: parole italiane comuni, in ordine approssimato di frequenza
// d'uso (dalle più frequenti). Senza accenti, come si scrivono su una
// tastiera di 26 lettere ("perche", "citta"). La frequenza di ciascuna è
// stimata dal rango con la legge di Zipf: è un modello di lingua piccolo ma
// realistico per la struttura delle parole italiane.
const lexicon = `
di e il la che a in un per non una del si sono con le da i lo al ma come
anche se piu della questo ha mi ci io gli dei nel essere alla tu ne cosa
ti quando lui me era fatto molto tutto gia lei solo fare ho dove noi
bene ora sempre tempo chi ancora detto poi casa vita anni prima tutti
qui poco vero oggi niente dopo grazie stato due giorno mondo cose parte
nella stesso proprio questa fa lavoro sei siamo nulla altro tanto sua
mio mia suo tuo loro voi abbiamo hanno vuoi voglio posso puoi deve devo
uomo donna padre madre figlio figlia amico amica amore paese citta
strada acqua mano occhi cuore testa notte sera mattina giorni settimana
mese anno volta volte modo cosi perche forse qualcosa nessuno ognuno
dire andare vedere sapere venire dare stare trovare parlare pensare
lasciare prendere guardare sentire mettere tornare chiamare portare
capire credere aspettare restare passare finire vivere morire entrare
uscire aprire chiudere leggere scrivere mangiare bere dormire giocare
lavorare correre camminare ascoltare aiutare cercare comprare pagare
studiare imparare insegnare ricordare dimenticare cambiare provare
grande piccolo nuovo vecchio bello brutto buono cattivo lungo corto
alto basso caldo freddo forte debole facile difficile giusto sbagliato
primo ultimo vicino lontano pieno vuoto aperto chiuso felice triste
stanco pronto libero sicuro possibile importante diverso stessa
famiglia bambino bambini ragazzo ragazza persone persona gente medico
scuola libro lettera parola parole nome storia guerra pace fine inizio
problema domanda risposta momento posto luogo punto lato fronte corpo
voce viso piede braccio gamba sangue pelle capelli bocca naso orecchio
sole luna cielo terra mare fiume lago monte montagna campo albero fiore
erba pietra fuoco aria vento pioggia neve tempesta estate inverno
primavera autunno colore bianco nero rosso verde giallo blu azzurro
cibo pane pasta pizza carne pesce frutta latte vino birra caffe acqua
tavolo sedia letto porta finestra muro stanza cucina bagno giardino
macchina treno aereo nave bicicletta autobus viaggio stazione piazza
chiesa ospedale negozio mercato ufficio banca albergo ristorante
telefono computer tastiera schermo lettore musica film foto immagine
denaro soldi prezzo costo lavoro tempo ora minuto secondo giornata
mattino pomeriggio domani ieri adesso subito presto tardi spesso
mai insieme fuori dentro sopra sotto dietro davanti contro senza verso
sempre quasi troppo meno tanto abbastanza davvero proprio infatti
allora quindi pero mentre invece oppure ecco certo bravo grazie prego
ciao buongiorno buonasera arrivederci scusa scusi aiuto basta attenzione
acqua dolore febbre medicina terapia dottore infermiere sedia rotelle
voglio bere voglio mangiare ho sete ho fame ho freddo ho caldo sto bene
sto male chiamare aprire spegnere accendere luce televisione radio
finestra coperta cuscino girare alzare abbassare sedere sdraiare
mamma papa nonno nonna fratello sorella marito moglie zio zia cugino
cane gatto cavallo uccello animale natura paesaggio isola spiaggia
festa regalo compleanno natale vacanza viaggio sogno speranza paura
gioia rabbia pensiero idea ragione verita bugia segreto mistero
governo stato legge diritto lavoro politica economia mercato
societa cultura arte scienza teatro cinema libro romanzo poesia
calcio partita squadra gioco sport palla vittoria sconfitta campione
memoria mente anima spirito forza potere energia luce ombra silenzio
rumore suono canzone ballo danza cantare suonare ridere piangere
sorridere abbracciare baciare toccare odiare amare sperare temere
domenica lunedi martedi mercoledi giovedi venerdi sabato gennaio
febbraio marzo aprile maggio giugno luglio agosto settembre ottobre
novembre dicembre numero cento mille milione tre quattro cinque sei
sette otto nove dieci undici dodici venti trenta mezzo doppio
`

// excluded: forme senza accento o troppo brevi o funzionali, che non
// compaiono come parole da copiare nei blocchi di prova (restano nel
// modello di lingua).
var excluded = map[string]bool{
	"perche": true, "cosi": true, "gia": true, "piu": true, "citta": true,
	"pero": true, "caffe": true, "papa": true, "societa": true, "verita": true,
	"lunedi": true, "martedi": true, "mercoledi": true, "giovedi": true,
	"venerdi": true, "della": true, "nella": true, "questo": true, "questa": true,
	"anche": true, "quando": true, "essere": true, "abbiamo": true, "hanno": true,
}
