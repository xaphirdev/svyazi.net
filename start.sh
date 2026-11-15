#!/bin/sh
# One line intentionally: normalize CRLF before the shell parses the rest.
if LC_ALL=C grep -q "$(printf '\r')" "$0"; then sed -i 's/\r$//' "$0"; exec sh "$0" "$@"; fi # CRLF repair
set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$PROJECT_DIR"
if [ "$(id -u)" -ne 0 ]; then
    command -v sudo >/dev/null 2>&1 || { echo 'Запустите скрипт от root.' >&2; exit 1; }
    exec sudo sh "$PROJECT_DIR/start.sh" "$@"
fi

for file in deploy/*.sh deploy/*.init deploy/*.openrc deploy/*.service scripts/mariadb-init.sh scripts/migrate-databases.sh; do sed -i 's/\r$//' "$file"; done
. ./deploy/common.sh
. ./deploy/docker.sh
. ./deploy/config.sh
trap 'status=$?; if [ "$status" -ne 0 ]; then deployment_failed "$status"; fi' 0

check_host
ensure_docker
prepare_env
chown 65532:65532 .local/credentials
compose config --quiet

log 'Запускаю MariaDB.'
compose up -d db
wait_database_server
# Init scripts are skipped by MariaDB on existing volumes, so provision them here too.
compose exec -T db sh /docker-entrypoint-initdb.d/01-databases.sh
wait_database

log 'Собираю и запускаю приложение.'
compose up --build -d app
wait_application

log 'Настраиваю доступ к опубликованным портам.'
sh deploy/firewall.sh "$APP_PORT"
compose ps
install -m 0600 .local/credentials/users-passwords.tsv accounts-for-reviewers.tsv
printf '\nПриложение: http://<IP-виртуалки>:%s\nMariaDB: только внутренняя сеть Docker, порт на хосте не опубликован.\n' "$APP_PORT"
printf 'Учётные записи: %s/.local/credentials/users-passwords.tsv\n' "$PROJECT_DIR"
printf 'Файл для проверяющих: %s/accounts-for-reviewers.tsv\n' "$PROJECT_DIR"
printf 'Если сайт недоступен с другого компьютера, проверьте NAT и firewall гипервизора.\n'
