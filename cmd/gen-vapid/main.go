// Genera un par de claves VAPID para Web Push.
// Uso: go run ./cmd/gen-vapid
// Copia la salida a tu .env (una sola vez).
package main

import (
	"fmt"
	"log"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatalf("GenerateVAPIDKeys: %v", err)
	}
	fmt.Printf("VAPID_PUBLIC_KEY=%s\n", publicKey)
	fmt.Printf("VAPID_PRIVATE_KEY=%s\n", privateKey)
	fmt.Printf("VAPID_EMAIL=mailto:contacto@dezai.mx\n")
}
