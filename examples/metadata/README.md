# Metadata и процессы

Пример вызывает методы `Client` для пяти metadata/process actions. Запросы
отправляются как `POST application/x-www-form-urlencoded`; программа выводит
только значение `data` из успешного envelope без интерпретации его схемы.

Задайте полный URL API и credentials через окружение:

```sh
export RUKOVODITEL_ENDPOINT='https://test.example.org/api/rest.php'
export RUKOVODITEL_KEY='...'
export RUKOVODITEL_USERNAME='...'
export RUKOVODITEL_PASSWORD='...'
```

Запросы для чтения:

```sh
go run ./examples/metadata -action get_entities
go run ./examples/metadata -action get_entity_fields -entity-id 21
go run ./examples/metadata -action get_field_choices -entity-id 21 -field-id 157
go run ./examples/metadata -action get_process_buttons -entity-id 21
go run ./examples/metadata -action get_process_buttons -entity-id 21 -item-id 1
```

`run_process` может менять данные. Запускайте его только на подготовленной
тестовой записи с безопасным процессом:

```sh
go run ./examples/metadata -action run_process -entity-id 21 -item-id 1 -process-id 7 -allow-write
```

При ошибке программа печатает только её вид и статус. Сырое тело доступно
вызывающему коду через `HTTPError.Body` или `APIError.Body`; оно может
содержать данные сервера, поэтому пример не выводит его автоматически.
Ответы конкретного сервера можно документировать после обезличивания,
указав версию, запрос и границы наблюдения. Описание transport и actions:
[API.md](../../docs/API.md). Наблюдавшиеся ключи `get_entity_fields`,
варианты и полный список `type` для сущностей 1 и 39:
[FIELDS.md](../../docs/FIELDS.md).
