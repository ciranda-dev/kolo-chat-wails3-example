package main

import (
	"fmt"
	"strings"
)

// trayStrings are the labels of the tray menu, in the three languages of the window.
var trayStrings = map[string]map[string]string{
	"en": {
		"open":       "Open Kolo Chat",
		"notch":      "Show messages in the notch",
		"quit":       "Quit",
		"peers0":     "No peers online",
		"peers1":     "1 peer online",
		"peersN":     "%d peers online",
		"pinOK":      "pin ready",
		"pinError":   "pin unavailable",
		"pinWaiting": "messages not opened yet: %d",
		"unread1":    "1 unread message",
		"unreadN":    "%d unread messages",
	},
	"es": {
		"open":       "Abrir Kolo Chat",
		"notch":      "Mostrar mensajes en el notch",
		"quit":       "Salir",
		"peers0":     "Ningún par en línea",
		"peers1":     "1 par en línea",
		"peersN":     "%d pares en línea",
		"pinOK":      "pin listo",
		"pinError":   "pin no disponible",
		"pinWaiting": "mensajes aún sin abrir: %d",
		"unread1":    "1 mensaje sin leer",
		"unreadN":    "%d mensajes sin leer",
	},
	"pt": {
		"open":       "Abrir Kolo Chat",
		"notch":      "Mostrar mensagens no notch",
		"quit":       "Sair",
		"peers0":     "Nenhum par online",
		"peers1":     "1 par online",
		"peersN":     "%d pares online",
		"pinOK":      "pin pronto",
		"pinError":   "pin indisponível",
		"pinWaiting": "mensagens ainda não abertas: %d",
		"unread1":    "1 mensagem não lida",
		"unreadN":    "%d mensagens não lidas",
	},
}

// pickLanguage maps a language tag such as "pt-BR" to one of the languages we have, English when it is none.
func pickLanguage(tag string) string {
	lang, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(tag, "_", "-")), "-")
	if _, ok := trayStrings[lang]; ok {
		return lang
	}
	return "en"
}

// count picks the label for n: key+"0" and key+"1" when they exist, key+"N" otherwise.
func count(lang, key string, n int) string {
	s := trayStrings[lang]
	if n <= 1 {
		if v, ok := s[fmt.Sprint(key, n)]; ok {
			return v
		}
	}
	return fmt.Sprintf(s[key+"N"], n)
}
