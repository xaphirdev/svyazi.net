#!/bin/sh
# Shared output and bounded readiness checks. No side effects on source.
log() { printf '\n[svyazi.net] %s\n' "$*"; }
die() { printf '\n[svyazi.net] Ошибка: %s\n' "$*" >&2; exit 1; }

compose() {
    if [ "$COMPOSE" = plugin ]; then docker compose "$@"; else docker-compose "$@"; fi
}

check_host() {
    [ -r /etc/os-release ] || die 'Не найден /etc/os-release.'
    . /etc/os-release
    case " ${ID:-} ${ID_LIKE:-} " in
        *' debian '*|*' ubuntu '*) ;;
        *) [ -f /etc/debian_version ] || die 'Нужен дистрибутив семейства Debian/Ubuntu.' ;;
    esac
    command -v apt-get >/dev/null 2>&1 && command -v dpkg >/dev/null 2>&1 || die 'Не найдены APT и dpkg.'
    [ "$(dpkg --print-architecture)" = amd64 ] || die 'MariaDB 10.1.48 в этом проекте требует amd64.'
    [ -f compose.yaml ] && [ -f Dockerfile ] || die 'Запускайте start.sh из полного проекта.'
    log "Система: ${PRETTY_NAME:-${ID:-Debian-compatible}} ($(dpkg --print-architecture))"
}

detect_init() {
    if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
        INIT_SYSTEM=systemd
    elif command -v rc-service >/dev/null 2>&1; then
        INIT_SYSTEM=openrc
    elif command -v service >/dev/null 2>&1; then
        INIT_SYSTEM=sysv
    else
        INIT_SYSTEM=none
    fi
}

deployment_failed() {
    printf '\n[svyazi.net] Развёртывание остановлено (код %s). Данные и контейнеры сохранены.\n' "$1" >&2
    if [ -n "${COMPOSE:-}" ]; then
        compose ps >&2 || true
        compose logs --tail=40 db app >&2 || true
    fi
}

wait_database_server() {
    elapsed=0
    while [ "$elapsed" -lt 240 ]; do
        if compose exec -T db sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=tcp -h127.0.0.1 -uroot -Nse "SELECT 1"' >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
        elapsed=$((elapsed+2))
    done
    die 'Сервер MariaDB не отвечает. Проверьте root-пароль существующего тома в .env.'
}

wait_database() {
    elapsed=0
    while [ "$elapsed" -lt 240 ]; do
        container=$(compose ps -q db)
        if [ -n "$container" ]; then
            state=$(docker inspect --format '{{.State.Status}}/{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container")
            case "$state" in
                running/healthy) return 0 ;;
                exited/*|dead/*|*/unhealthy) break ;;
            esac
        fi
        sleep 2
        elapsed=$((elapsed+2))
    done
    die 'MariaDB не готова. Проверьте логи и пароли существующего тома в .env. Firewall не исправляет ошибку инициализации БД.'
}

wait_application() {
    deadline=$(($(date +%s)+180))
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if curl --noproxy '*' --fail --silent --max-time 3 "http://127.0.0.1:$APP_PORT/healthz" >/dev/null; then
            [ -s .local/credentials/users-passwords.tsv ] || die 'Не создан файл пользователей и паролей.'
            return 0
        fi
        sleep 2
    done
    die 'Приложение не прошло HTTP-проверку /healthz за 180 секунд.'
}
