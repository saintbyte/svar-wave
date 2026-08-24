# svar-wave

Веб-сервис на Go: принимает аудиофайлы по HTTP и воспроизводит их на сервере.
HTTP — [fiber v3](https://github.com/gofiber/fiber), звук — [beep v2](https://github.com/gopxl/beep).
Поддерживаемые форматы: **WAV, MP3, FLAC, OGG (Vorbis)**.

## Возможности

- загрузка файлов по HTTP (multipart), опционально с немедленным воспроизведением
- управление плеером: play / pause / stop, живой статус с позицией
- конфигурация через YAML
- режим демона (двойной fork, pidfile, лог в файл)
- graceful shutdown по SIGTERM/SIGINT
- встроенная тестовая HTML-страница (go:embed, бинарь полностью самодостаточен)

## Сборка

Требуется Go 1.25+.

```bash
make build        # нативная сборка -> bin/svar-wave
make release      # кросс-сборка под linux-amd64 + linux-arm64 + linux-arm7
```

Аудио-вывод (beep/oto) на Linux требует cgo и ALSA, поэтому:

| Цель          | Что нужно на хосте                                        | Результат                    |
|---------------|-----------------------------------------------------------|------------------------------|
| `make build`  | gcc + `libasound2-dev`                                     | `bin/svar-wave`              |
| `make amd64`  | gcc + `libasound2-dev`                                     | `bin/svar-wave-linux-amd64`  |
| `make arm64`  | `gcc-aarch64-linux-gnu`                                    | `bin/svar-wave-linux-arm64`  |
| `make arm7`   | `gcc-arm-linux-gnueabihf`                                  | `bin/svar-wave-linux-arm7`   |

Для ARM-целей make сам скачает deb-пакеты ALSA нужной архитектуры
с deb.debian.org и развернёт их в `.sysroots/` (можно стереть: `make distclean`).
Имена кросс-компиляторов определяются автоматически; переопределить:

```bash
make arm7 CC_ARMV7=arm-linux-gnueabihf-gcc-14
```

Готовые бинари динамически линкуются с `libasound.so.2`, которая есть
на любом обычном Linux (Raspberry Pi OS, Debian, Ubuntu и т.д.).

## Запуск

```bash
make run          # foreground
./bin/svar-wave -config config.yaml
./bin/svar-wave   # без -config ищет config.yaml в текущем каталоге
```

Флаги:

| Флаг        | Описание                                        |
|-------------|-------------------------------------------------|
| `-config`   | путь к YAML-конфигу (по умолчанию `config.yaml`)|
| `-daemon`   | уйти в фон, записать pidfile                    |
| `-stop`     | послать SIGTERM процессу из pidfile             |
| `-version`  | показать версию                                 |

Демон:

```bash
./bin/svar-wave -config config.yaml -daemon   # старт
./bin/svar-wave -config config.yaml -stop     # остановка
tail -f svar-wave.log                         # логи
```

systemd: см. `scripts/svar-wave.service` (запускает сервис в foreground,
рестартами управляет systemd).

## Конфигурация

`config.yaml` (все ключи имеют разумные значения по умолчанию):

```yaml
http:
  host: "0.0.0.0"
  port: 8080
  body_limit_mb: 100      # максимальный размер загрузки

storage:
  dir: "./uploads"        # каталог для загруженных файлов

player:
  sample_rate: 44100      # всё приводится к этой частоте
  buffer_ms: 200
  resample_quality: 3     # качество ресемплера beep (1..4)

daemon:
  pid_file: "/tmp/svar-wave.pid"
  log_file: "./svar-wave.log"  # пусто = логи демона отбрасываются
  umask: 027

upload:
  autoplay: true          # играть сразу после загрузки

log:
  level: "info"           # debug | info | warn | error
```

## HTTP API

| Метод | Путь            | Описание                                             |
|-------|-----------------|------------------------------------------------------|
| GET   | `/`             | тестовая страница                                     |
| POST  | `/upload`       | multipart, поле `file`; `?autoplay=1/0` переопределяет конфиг |
| GET   | `/files`        | список загруженных файлов                             |
| POST  | `/play/{name}`  | воспроизвести файл из хранилища                       |
| POST  | `/pause`        | пауза / продолжить                                    |
| POST  | `/stop`         | остановить воспроизведение                            |
| GET   | `/status`       | статус плеера                                         |
| GET   | `/healthz`      | health-check                                          |

Примеры:

```bash
# загрузить и сразу играть
curl -F "file=@song.mp3" localhost:8080/upload

# загрузить без автовоспроизведения
curl -F "file=@song.mp3" "localhost:8080/upload?autoplay=false"

curl -X POST localhost:8080/play/song.mp3
curl -X POST localhost:8080/pause
curl -X POST localhost:8080/stop
curl -s localhost:8080/status
# {"playing":true,"paused":false,"file":"song.mp3","position_ms":1200,"total_ms":214000}
```

Ответы — JSON; ошибки — `{"message": "..."}` с соответствующим HTTP-кодом.
Имена файлов очищаются от путей и опасных символов (path traversal исключён).

## Веб-интерфейс

На `http://localhost:8080/` доступна тестовая страница: загрузка файла
с прогресс-баром и переключателем «играть сразу», список файлов
с кнопками воспроизведения, pause/stop и статус плеера, обновляемый раз в секунду.

## Разработка

```bash
make vet fmt fmt-check test
```

Структура:

```
cmd/svarwave/main.go        # входная точка, флаги, демон, сигналы
internal/config/config.go   # YAML-конфиг + дефолты
internal/player/player.go   # beep-плеер: декодеры, ресемплинг, состояние
internal/server/server.go   # маршруты fiber v3
internal/server/web/        # встроенная тестовая страница
scripts/svar-wave.service   # systemd unit
Makefile                    # сборка: нативная + linux-amd64/arm64/arm7
```

## Замечания

- Одновременно воспроизводится один файл; новая загрузка/play прерывает текущий трек.
- Звук выводится на локальное устройство сервера (ALSA/PulseAudio); на машине без аудио `speaker.Init` вернёт ошибку, upload будет работать, а play вернёт 500.
- MP3/FLAC/OGG декодируются потоково; WAV — целиком в память.

## Лицензия

MIT — см. [LICENSE](LICENSE).
