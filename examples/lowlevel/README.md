# Ручная проверка низкоуровневого API

Команда выполняет ровно один `Client.Do` запрос. Она не запускается из
`go test ./...`; для обращения к тестовому Rukovoditel нужны переменные
окружения и явный `-action`. По умолчанию используется form transport.

```sh
export RUKOVODITEL_ENDPOINT='https://test.example.org/api/rest.php'
export RUKOVODITEL_KEY='...'
export RUKOVODITEL_USERNAME='...'
export RUKOVODITEL_PASSWORD='...'

go run ./examples/lowlevel -action get_entities
go run ./examples/lowlevel -action select -entity-id 21
```

Для дополнительных параметров создайте локальный JSON-файл вне репозитория:

```json
{"filters":{"id":"13,14"},"select_fields":"338,388","rows_per_page":10}
```

```sh
go run ./examples/lowlevel -action select -entity-id 21 -params-file /tmp/select-params.json
```

Пример для проверки формата `data` у update на специально созданной записи:

```json
{"data":{"field_338":"Проверка"},"update_by_field":{"id":37}}
```

```sh
go run ./examples/lowlevel -action update -entity-id 21 -params-file /tmp/update-params.json -allow-write
```

`insert`, `update`, `delete` и `run_process` требуют `-allow-write`.
Низкоуровневый `Do` не проверяет критерии массового update/delete; перед
запуском проверьте JSON и используйте только специально созданные записи.

Команда печатает `status` и сырой `data`; при ошибке API или HTTP печатает
сырое тело ответа. Оно может содержать персональные данные и секреты,
возвращённые сервером. Перед добавлением результата в `docs/API.md`
удалите их и укажите версию сервера, action, параметры, HTTP status и
границы сделанного вывода. Не сохраняйте credentials или сырой вывод в git.
