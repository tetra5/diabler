# diabler
Diablo IV themed Telegram bot.

## Building
```sh
docker build --tag diabler .
```

## Running
```sh
docker run -v ./diabler-data:/data --env TELEGRAM_TOKEN="<your_token>" diabler
```
