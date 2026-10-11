# rukovoditel-go

Go-библиотека для интеграций с REST API Rukovoditel. Три публичных пакета:
[`api`](api) отправляет actions и сохраняет wire format;
[`schema`](schema) загружает metadata и привязывает Go-структуры к явным ID;
[`orm`](orm) выполняет ограниченный набор типизированных операций.
ID сущностей и полей зависят от установки. Сверьте их со своей схемой перед
использованием примеров.

Первая версия охватывает итерации 0–12 из [плана](docs/PLAN.md):
низкоуровневые actions, явное обновление metadata, привязку моделей и
ограниченные типизированные запросы. Частичное обновление и обновление
по фильтрам остаются будущими итерациями. Подтверждённый REST-формат,
поведение Go-клиента и открытые вопросы разделены между
[API.md](docs/API.md), [CLIENT.md](docs/CLIENT.md) и
[RESEARCH.md](docs/RESEARCH.md).

Для одного подключения и нескольких моделей доступен фасад корневого
пакета: `rukovoditel.New(api.Config{...})` создаёт общий клиент и кэш;
`Refresh(ctx, entityID)` явно загружает схему, `Register(entityID, Model{})`
привязывает тип, а `rukovoditel.Repository[Model](connection)` возвращает
типизированный репозиторий. `connection.Client()` открывает тот же
низкоуровневый клиент. Пример с двумя моделями — в
[`examples/facade`](examples/facade/main.go). ID указываются явно;
обновление схемы выполняется только по вызову `Refresh`.

Чтобы узнать ID в своей установке, запустите отдельную команду
[`rukovoditel-inspect`](cmd/rukovoditel-inspect/README.md). Она позволяет
выбрать сущность, изучить её поля и записи, раскрыть отдельное поле
или запись. Список записей показывает один ответ сервера. В Go-коде
для списка доступных сущностей есть `SchemaCache.ListEntities(ctx)`.

## Установка и конфигурация

В Go-модуле приложения:

```sh
go get github.com/spanditime/rukovoditel-go
```

Нужен Go версии из [go.mod](go.mod) или новее. Передавайте полный URL API
(например, `https://example.org/api/rest.php`) и credentials из настроек
приложения. `NewClient` проверяет их наличие без сетевого запроса.
По умолчанию используется form POST и HTTP timeout 10 секунд; свой
`*http.Client` можно передать через `Config.HTTPClient`.

```go
client, err := api.NewClient(api.Config{
    Endpoint: os.Getenv("RUKOVODITEL_ENDPOINT"),
    Key: os.Getenv("RUKOVODITEL_KEY"),
    Username: os.Getenv("RUKOVODITEL_USERNAME"),
    Password: os.Getenv("RUKOVODITEL_PASSWORD"),
})
if err != nil { log.Fatal(err) }
```

Все сетевые методы принимают `context.Context`. Используйте HTTPS для
credentials. JSON transport выбирается через `Config.Encoding = api.JSONEncoding`,
но он документирован как минимум для `select`;
поддержка каждого другого action требует отдельной проверки.

## Основной пример: ORM

Запускаемый вариант находится в [examples/orm](examples/orm/README.md).
Он только читает запись. Замените `39`, `418` и `ITEM_ID` на ID
вашей установки. Содержимое поля остаётся сырым JSON: библиотека не
угадывает его Go-тип. Код ниже можно поместить в `main.go` отдельного
модуля.

После настройки `RUKOVODITEL_ENDPOINT`, `RUKOVODITEL_KEY`,
`RUKOVODITEL_USERNAME` и `RUKOVODITEL_PASSWORD` запустите:

```sh
go run ./examples/orm -item-id 123
```

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "os"
    "strconv"

    "github.com/spanditime/rukovoditel-go/api"
    "github.com/spanditime/rukovoditel-go/orm"
    "github.com/spanditime/rukovoditel-go/schema"
)

type Task struct {
    ID    int64                      `rukovoditel:"id"`
    Title json.RawMessage            `rukovoditel:"418"`
    Extra map[string]json.RawMessage `rukovoditel:"-,raw"`
}

func main() {
    ctx := context.Background()
    client, err := api.NewClient(api.Config{
        Endpoint: os.Getenv("RUKOVODITEL_ENDPOINT"),
        Key: os.Getenv("RUKOVODITEL_KEY"),
        Username: os.Getenv("RUKOVODITEL_USERNAME"),
        Password: os.Getenv("RUKOVODITEL_PASSWORD"),
    })
    if err != nil { log.Fatal(err) }

    // Загрузить схему и привязать Go-модель к явным ID.
    cache, err := schema.NewSchemaCache(client)
    if err != nil { log.Fatal(err) }
    if _, err := cache.Refresh(ctx, 39); err != nil { log.Fatal(err) }
    if _, err := cache.Register(39, Task{}); err != nil { log.Fatal(err) }

    // Прочитать одну запись через ORM.
    repo, err := orm.NewRepository[Task](client, cache)
    if err != nil { log.Fatal(err) }
    id, err := strconv.ParseInt(os.Getenv("ITEM_ID"), 10, 64)
    if err != nil { log.Fatal(err) }
    task, err := repo.Get(ctx, id)
    if err != nil { log.Fatal(err) }
    log.Printf("item %d; title JSON bytes: %d", task.ID, len(task.Title))
}
```

## Публичный API и границы

| Пакет | Основные вызовы | Возвращаемое значение и граница |
| --- | --- | --- |
| `api` | `NewClient`, `Client.Do`, `Insert`, `Select`, `Update`, `Delete` | Form POST по умолчанию; `Do` возвращает `Response` с `json.RawMessage Data`; `Select` сохраняет ключи полей вроде `"418"`. |
| `api` | `GetEntities`, `GetEntityFields`, `GetFieldChoices`, `GetProcessButtons`, `RunProcess` | `json.RawMessage`; структура `data` зависит от action. `RunProcess` может менять данные. |
| `schema` | `NewSchemaCache`, `Refresh`, `RefreshAll`, `Get`, `Register`, `ModelFor` | Снимки metadata по явному entity ID; обновление только по вызову. `Model.Decode` и `EncodeWrite` выполняют преобразование без HTTP. |
| `orm` | `NewRepository[T]`, `Get`, `Insert`, `Update`, `Delete`, `Query().Search().RowsPerPage().All()` | Типизированные операции по зарегистрированной структуре. `Sort` возвращает ошибку; `All` читает только один ответ сервера. |

Для модели используйте экспортируемые поля и теги `rukovoditel:"<ID>"`.
Поле `rukovoditel:"id"` может иметь тип `int64`, `json.RawMessage`
или `*json.RawMessage`. Пользовательское поле принимает `json.RawMessage`
или `*json.RawMessage`. Тег `rukovoditel:"-,raw"` на
`map[string]json.RawMessage` сохраняет непривязанные ключи ответа.
Неизвестные поля без этого тега нужно сохранять из исходной строки самому.
Отсутствующий ключ остаётся нулевым значением, JSON `null` сохраняется
как сырое `null`.

`EncodeWrite` поддерживает только JSON-строки для полей metadata с типом
`fieldtype_input`. Нулевое/отсутствующее значение опускается, пустая
JSON-строка передаётся явно. JSON `null`, другие типы полей, связи и
вложения отклоняются. Сервер решает, доступно ли поле для записи;
metadata не доказывает это. В `orm` `Insert` требует хотя бы одно
кодируемое поле. Для сущности с обязательными неподдержанными полями
используйте `api.Client.Do` и подтверждённый для неё wire format.

`Query.Search("Title", "text")` переводит имя поля Go-структуры в ID
поля и отправляет документированный `condition=search`.
`RowsPerPage(n)` задаёт размер одного ответа. Имя параметра следующей
страницы и произвольная сортировка не подтверждены, поэтому полного
обхода страниц в `orm` нет. `api.Select` также предоставляет
документированные `Filters`, `SelectFields`, `Limit` и `RowsPerPage`.
Низкоуровневый `Client.Do` позволяет исследовать дополнительные параметры
без объявления их стабильным контрактом.

`Update` и `Delete` могут затронуть несколько записей. В `orm` передайте
один или несколько положительных ID; пустой список отклоняется.
`api.Update` и `api.Delete` требуют непустого критерия, но непустой
критерий сам по себе не гарантирует единственную запись. Проверяйте
результат повторным чтением там, где важен эффект операции.

## Ошибки и обновление схемы

`api.HTTPError` означает HTTP не-2xx, `api.APIError` — ответ с
`status != "success"`. Их можно различать через `errors.As`.
`Body` и `Data` сохраняют сырой ответ сервера и могут содержать
чувствительные сведения; не выводите их в лог без очистки.
Ошибки кодирования и транспорта оборачивают исходную причину для
`errors.Is`/`errors.As`, но текст ошибки скрывает credentials.
Ошибки `schema` и `orm` описывают локальную проверку или возвращают
ошибку клиента.

У `SchemaCache` нет TTL или фонового обновления. После изменения схемы
явно вызовите `Refresh(ctx, entityID)` либо `RefreshAll(ctx)`.
Успешное обновление заменяет снимок целиком; ошибка сохраняет прежний.
Зарегистрированная модель блокирует обновление, если привязанное поле
удалено, его тип изменился или зарегистрированная сущность исчезла.
При намеренной миграции исправьте теги и типы модели под новую схему,
создайте новый `SchemaCache`, загрузите metadata и зарегистрируйте
новую модель. Для другой установки ID в тегах нужно указать заново:
стабильность ID между окружениями не установлена.

## Проверки и документация

`go test ./...` выполняет локальные тесты без обращения к серверу.
Серверные сценарии вынесены в команду `examples/probe` и запускаются
только явно; они не входят в тестовый набор пакета.
[Ручной opt-in probe](examples/probe/README.md) описывает проверку на
тестовом сервере с отдельным разрешением записи. Сервер 3.7 подтвердил
сценарии чтения и записи, перечисленные в [RESEARCH.md](docs/RESEARCH.md);
это наблюдения одной версии и одного API-пользователя.

- [REST-формат API](docs/API.md)
- [Публичный Go-клиент первой версии](docs/CLIENT.md)
- [Наблюдавшиеся формы полей](docs/FIELDS.md)
- [Открытые вопросы и результаты probe](docs/RESEARCH.md)
- [План и статусы итераций](docs/PLAN.md)
- [Инструмент просмотра схемы](cmd/rukovoditel-inspect/README.md)
- [Основной пример ORM](examples/orm/README.md)
- [Низкоуровневый пример](examples/lowlevel/README.md)
- [Metadata и process actions](examples/metadata/README.md)

Не подтверждены единый формат error responses, имя параметра страницы,
сортировка, batch insert, wire format связей и вложений, типы всех
значений ответа, непустые global lists и эффекты `run_process`.
Список наблюдений и границ вывода приведён в [RESEARCH.md](docs/RESEARCH.md).
