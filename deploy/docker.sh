#!/bin/sh
. ./deploy/repository.sh

package_available() {
    LC_ALL=C apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ && $2 != "(none)" {available=1} END {exit !available}'
}

start_docker() {
    detect_init
    case "$INIT_SYSTEM" in
        systemd) systemctl enable --now docker ;;
        openrc)
            rc-service docker start
            rc-update add docker default
            ;;
        sysv)
            service docker start
            if command -v update-rc.d >/dev/null 2>&1; then update-rc.d docker defaults; fi
            ;;
        none)
            docker info >/dev/null 2>&1 || die 'Нет менеджера служб. Запустите Docker Engine вручную и повторите запуск.'
            ;;
    esac
    docker info >/dev/null || die 'Docker Engine недоступен. Проверьте службу docker и её журнал.'
}

ensure_docker() {
    # HTTPS tools are needed before adding the signed APT repository.
    set --
    for package in ca-certificates curl; do
        if ! dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q '^install ok installed$'; then
            set -- "$@" "$package"
        fi
    done
    if [ "$#" -gt 0 ]; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
    fi
    add_docker_repository
    apt-get update
    set --
    if ! command -v docker >/dev/null 2>&1; then
        set -- docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    elif ! docker compose version >/dev/null 2>&1 && ! docker-compose version >/dev/null 2>&1; then
        # A distribution-packaged engine is kept in place; avoid a conflicting CE CLI.
        if dpkg-query -W -f='${Status}' docker-ce-cli 2>/dev/null | grep -q '^install ok installed$'; then
            set -- docker-compose-plugin
        elif package_available docker-compose-v2; then
            set -- docker-compose-v2
        elif package_available docker-compose; then
            set -- docker-compose
        else
            die 'Для установленного Docker не найден совместимый Compose.'
        fi
    fi
    if ! command -v iptables >/dev/null 2>&1; then set -- "$@" iptables; fi
    if [ "$#" -gt 0 ]; then
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
    fi
    start_docker
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=plugin
    elif command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; then
        COMPOSE=standalone
    else
        die 'Docker Compose не установлен.'
    fi
}
