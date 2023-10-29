# diabler
Diablo IV themed Telegram bot.
### No longer maintained as of Season 2.

## Cloning
```sh
git clone https://github.com/tetra5/diabler.git
```

## Building
```sh
docker build --tag diabler .
```

## Running
```sh
docker run -v ./diabler-data:/data --env TELEGRAM_TOKEN="<your_token>" -d --name diabler diabler
```
