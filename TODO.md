# TODO

## Security

- [ ] Вынести `FRPToken` из хардкода (`internal/frpc/config.go`). Токен должен приходить из API (ответ `/init`) или из переменной окружения (`QUDATA_FRPC_TOKEN`), а не быть зашит в исходный код.
