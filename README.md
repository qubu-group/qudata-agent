# QuData Agent

GPU-инстанс агент с QEMU/VFIO passthrough для эксклюзивного доступа к GPU.

## Архитектура

- **QEMU VM** запускается при старте агента с passthrough всех GPU
- **Контейнеры** создаются внутри VM по запросу через Docker
- **Туннелирование** через FRP для удалённого доступа

## Установка

```bash
curl -fsSL https://raw.githubusercontent.com/qubu-group/qudata-agent/main/install.sh | sudo bash -s -- ak-YOUR-API-KEY
```

Установщик автоматически:
1. Определяет все NVIDIA GPU
2. Настраивает IOMMU в GRUB
3. Загружает и кастомизирует базовый образ VM
4. Перезагружает систему (если требуется)
5. Запускает агент

### Debug Mode

Для разработки без GPU:

```bash
curl -fsSL ... | sudo bash -s -- ak-YOUR-API-KEY --debug
```

⚠️ В debug mode GPU passthrough отключен, используются mock данные.

## Требования

- Debian/Ubuntu
- NVIDIA GPU с поддержкой VFIO
- VT-d/AMD-Vi в BIOS
- 32GB+ RAM
- 50GB+ свободного места

## Конфигурация

Переменные окружения:

| Переменная | Описание | По умолчанию |
|-----------|----------|--------------|
| `QUDATA_API_KEY` | API ключ (обязательно) | - |
| `QUDATA_GPU_PCI_ADDRS` | PCI адреса GPU через запятую | auto |
| `QUDATA_BASE_IMAGE` | Путь к базовому образу | `/var/lib/qudata/images/qudata-base.qcow2` |
| `QUDATA_DEBUG` | Debug mode | `false` |

## Управление

```bash
# Статус
systemctl status qudata-agent

# Логи
journalctl -u qudata-agent -f

# Перезапуск
systemctl restart qudata-agent
```

## Структура

```
/var/lib/qudata/
├── images/           # qcow2 образы
├── .ssh/             # SSH ключи для VM
└── data/             # Данные инстансов

/var/run/qudata/      # QMP сокеты, runtime файлы
/var/log/qudata/      # Логи
/etc/qudata/          # FRP конфигурация
```
