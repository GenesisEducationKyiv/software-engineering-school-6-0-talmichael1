# Тестування

Три типи тестів, по одній команді на кожен. На машині треба тільки `git`, `docker` та Go — все інше (Postgres, Redis, бекенд, фронт, Chromium) піднімається з нуля в Docker.

## Юніт

```sh
go test -race -count=1 ./...
```

Звичайні `*_test.go` поряд із кодом. Без Docker.

## Інтеграційні

```sh
docker compose -f docker-compose.test.yml -p notifier-test up --exit-code-from tests
```

Піднімає чистий Postgres + контейнер з Go, який ганяє `go test -tags=integration` по `internal/integration/...` та `internal/repository/postgres/...`. GitHub і email замокані — креди не потрібні.

Прибрати після:

```sh
docker compose -f docker-compose.test.yml -p notifier-test down -v
```

## E2E

```sh
docker compose -f docker-compose.e2e.yml -p notifier-e2e up --build --exit-code-from tests
```

Піднімає весь стек (postgres + redis + app + nginx) + контейнер з Playwright-Go (Chromium всередині), який проганяє два сценарії в `tests/e2e/`:

1. Підписка через форму → бачимо success-alert.
2. Лукап email → бачимо репо в списку з бейджем «Pending».

GitHub-токен в стеку порожній, тож запити йдуть анонімно — двох тестів вистачає в межах 60 запитів/год.

Прибрати після:

```sh
docker compose -f docker-compose.e2e.yml -p notifier-e2e down -v
```

## Чому так

- **Два compose-файли замість одного.** `docker-compose.test.yml` — тільки Postgres для інтеграційних. `docker-compose.e2e.yml` — повний стек для E2E. Окремі проєкти (`-p notifier-test`, `-p notifier-e2e`), щоб не зачіпати dev-стек на стандартних портах.
- **Тести як сервіс у compose.** Раннер живе всередині docker-compose, `--exit-code-from tests` пробрасує його exit-код назовні. Так «одна команда» справді запускає все від нуля.
- **`playwright-go` лежить у головному `go.mod`.** Імпортується тільки під build-тегом `e2e`, тож у продакшн-бінарник не потрапляє. Окремий модуль для тестів цього розміру був би більшим геморроєм, ніж зайвий рядок у `go.sum`.
- **E2E в `tests/e2e/`, а інтеграційні в `internal/integration/`.** Інтеграційні дзвонять у `internal/...` напряму — Go дозволяє це робити тільки зсередини `internal/`. E2E ходить через справжній HTTP і нічого з `internal` не імпортує, тож живе вище.
- **Chromium і `localhost`.** Фронт у `web/index.html` дивиться на `window.location.hostname`, щоб обрати API-домен. У контейнері це було б `frontend`, і запити летіли б у прод. Тому тести стартують Chromium з `--host-resolver-rules="MAP localhost frontend"`: браузер реально йде в контейнер `frontend`, але `hostname` залишається `localhost` і фронт обирає відносний URL.

## CI

`.github/workflows/ci.yml` має по окремій job-і на кожен тип (`lint`, `unit`, `integration`, `e2e`) — паралеляться, фейл одного типу видно окремо. `build` залежить від усіх чотирьох.
