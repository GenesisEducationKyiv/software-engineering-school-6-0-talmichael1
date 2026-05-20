# ADR-0003: SOLID & GRASP rewrite

- **Статус:** Прийнято
- **Дата:** 2026-05-10

## Контекст

Пройшлися по коду з пошуком невідповідностей SOLID + GRASP. Знайшли певну кількість дрібних, але
наростаючих болячок: надмірні umbrella-інтерфейси, дубльований error-mapping
у хендлерах, великий `Subscribe`, `main.run()` на 150 рядків, кеш, що залежав
від конкретного клієнта, email-сендер з методом-на-тип-листа, URL-шаблони
розкидані по трьох файлах. Рефакторимо поки це ще просто. При цьому, шукали проблеми за SOLID&GRASP без фанатизму.

## Рішення

Один рефакторинг, кілька правок. Жодних змін у поведінці чи API.

| Принцип | Що було | Що стало |
|---|---|---|
| **ISP** | `repository.SubscriptionRepo` (8 методів), `RepositoryRepo` (5) на всіх споживачів | Маленькі інтерфейси на боці споживача: `subscriptionRepo`, `repoUpserter`, `scannerSubStore`, `scannerRepoStore`, `unconfirmedDeleter`. Файл `repository/interfaces.go` видалили. |
| **DIP** | `cache.NewCachedGitHubClient(*github.Client, …)` | Бере `cache.GitHubAPI`, пакет `cache` більше не імпортує `internal/github`. |
| **OCP / SRP** | `EmailSender` мав метод-на-тип-листа; `mailgun.Sender` змішував шаблони і транспорт | `email.Sender { Send(ctx, Message) }` + окремий `email.Templates`. Новий лист = новий метод у `Templates`, транспорт закритий. |
| **SRP** | `SubscriptionService.Subscribe` робив усе сам (валідація, GitHub-проба, upsert, токени, email, rollback) | Витягли `resolveRepo` і `newPendingSubscription`. `Subscribe` — оркестратор на ~20 рядків. |
| **OCP / DRY** | Однаковий `errors.Is` switch у 4 хендлерах + `grpc.mapError` | `handler.respondError(c, err, errorMessages{…})` — одне місце мапить domain-помилки в HTTP. |
| **SRP** | `cmd/server/main.go::run()` робив усе підряд | Витягли `connectDB`, `connectRedis`, `buildServices`, `buildRouter`, `serveGRPC`, `serveHTTP`. Заодно прибрали `os.Exit(1)` з goroutine — тепер серверні фейли йдуть в `errCh` і тригерять той самий graceful shutdown, що й SIGINT. Defers у `run()` гарантовано спрацьовують. |
| **GRASP — Information Expert** | URL-шаблони `/api/confirm/...` і `/api/unsubscribe/...` жили в трьох місцях | `internal/urls.Builder` — єдиний експерт з форми URL. |

## Наслідки

**Плюси:**
- Споживачі залежать тільки від того, що реально використовують —
  додавання методу одному сервісу не чіпає чужі тести й моки
  (`mockCleanupSubRepo` пішов з 9 методів до 1).
- Email-копірайт і транспорт еволюціонують незалежно.
- Один helper для error-mapping у хендлерах — додавання нової
  domain-помилки = одне місце правок, а не пʼять.
- `run()` — лінійна композиція з названих кроків. Кожен крок
  читається окремо.
- Серверні фейли більше не вбивають процес повз defers.

**Мінуси:**
- Дрібне дублювання інтерфейсів між пакетами (наприклад,
  `UpdateLastSeenTag` зустрічається і в `repoUpserter`, і в
  `scannerRepoStore`). Свідомо — краще ніж зчеплення через umbrella.
- Більше декларацій біля конструкторів сервісів, відповідно до рекомендованого Сергієм Воронкіним (Backend Team Lead, SKELAR)