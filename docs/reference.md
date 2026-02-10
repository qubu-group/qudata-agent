3. Детали реализации

3.1 Подготовка хоста и передача GPU в VM
 1. Включение TDX и IOMMU. Настроить BIOS так, чтобы TDX был активирован. В настройках ядра добавить параметры intel_iommu=on iommu=pt для включения поддержки IOMMU в режиме passthrough.
 2. Конфигурация VFIO. Определить PCI‑адрес GPU (lspci | grep -i nvidia), затем воспользоваться скриптом setup-gpu-vfio из репозитория Cocoon: ./scripts/setup-gpu-vfio --setup <pci-address> для отключения устройства от драйвера NVIDIA, включения CC‑режима и привязки к vfio-pci【372940314745023†L82-L99】. Скрипт также может восстановить конфигурацию после завершения работы.
 3. Настройка QEMU/Libvirt. Использовать следующую конфигурацию виртуальной машины:
 • Тип машины: q35 с confidential-guest-support;
 • Оперативная память: ≥ 32 ГБ для больших моделей;
 • Диск: read‑only rootfs (с dm‑verity), read‑write persistent образ;
 • Устройства: vfio‑pci для GPU (-device vfio-pci,host=<pci-address>,x-vfio-pci-confidential-gpu=on), virtio‑fs для монтирования /spec и /data;
 • VSOCK: для взаимодействия seal‑client с seal‑server и health‑client;
 • Сеть: virtio‑net с настройкой macfilter/iptables в VM для ограничения исходящих соединений.
Скрипт cocoon-launch должен быть модифицирован для добавления необходимых параметров Docker‑режима и проброса устройств /dev/nvidia* в гостевую VM.

3.2 Сборка гостевого образа

Образ должен строиться посредством mkosi (Debian Bookworm), как указано в документации Cocoon. Дополнительные шаги:
 1. Установка драйверов и зависимостей GPU. Установить официальный драйвер NVIDIA (открытая версия или проприетарная, совместимая с CC‑режимом), CUDA Toolkit, библиотеку libnvidia-container и nvidia-container-toolkit, позволяющие передавать GPU в контейнеры.
 2. Установка контейнерного рантайма. Включить Docker Engine или Podman. Для Docker необходимо установить containerd, runc и docker; для Podman — podman и conmon. Включить поддержку systemd и настроить сервис dockerd.service/podman.socket.
 3. Конфигурация docker‑daemon. Для Docker настроить /etc/docker/daemon.json так, чтобы разрешить использование GPU ("runtimes": {"nvidia": {"path": "/usr/bin/nvidia-container-runtime", "runtimeArgs": []}}). Задать прослушивание только на Unix‑сокете (/var/run/docker.sock) и запретить автоматическую загрузку непроверенных образов из общедоступных репозиториев.