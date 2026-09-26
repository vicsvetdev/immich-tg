// Command immich-tg forwards New Videos from Immich to the Telegram Video Channel.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"immich-tg/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := app.Main(ctx, os.Getenv, app.Options{})
	stop()
	os.Exit(code)
}
