#!/bin/sh

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
    # Package names differ between Debian, Ubuntu and their derivatives.
    need_engine=1
    need_compose=1
    engine_package=''
    if command -v docker >/dev/null 2>&1; then
        need_engine=0
        if docker compose version >/dev/null 2>&1; then need_compose=0; fi
    fi
    if command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; then need_compose=0; fi
    set --
    for package in ca-certificates curl iptables; do
        if ! dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q '^install ok installed$'; then
            set -- "$@" "$package"
        fi
    done
    if [ "$#" -gt 0 ] || [ "$need_engine" = 1 ] || [ "$need_compose" = 1 ]; then
        apt-get update
        if [ "$need_engine" = 1 ]; then
            if package_available docker.io; then
                engine_package=docker.io
                set -- "$@" docker.io
            elif package_available docker-ce; then
                engine_package=docker-ce
                set -- "$@" docker-ce docker-ce-cli containerd.io
            else
                die 'В подключённых APT-репозиториях нет Docker Engine. Проверьте источники пакетов системы.'
            fi
        fi
        if [ "$need_compose" = 1 ]; then
            compose_package=''
            compose_candidates='docker-compose-v2 docker-compose docker-compose-plugin'
            if [ "$engine_package" = docker-ce ] || dpkg-query -W -f='${Status}' docker-ce-cli 2>/dev/null | grep -q '^install ok installed$'; then
                compose_candidates='docker-compose-plugin docker-compose-v2 docker-compose'
            fi
            for candidate in $compose_candidates; do
                if package_available "$candidate"; then compose_package=$candidate; break; fi
            done
            [ -n "$compose_package" ] || die 'В подключённых APT-репозиториях нет Docker Compose.'
            set -- "$@" "$compose_package"
        fi
        log "Устанавливаю пакеты: $*"
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
