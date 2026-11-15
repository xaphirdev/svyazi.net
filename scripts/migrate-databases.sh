#!/bin/sh
set -eu

# Discover earlier application schemas by their tables, not by a hardcoded brand.
# All tables move together; a populated destination is never overwritten.
root_sql() { MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --batch --skip-column-names "$@"; }

source_users=$(root_sql -e "SELECT TABLE_SCHEMA FROM INFORMATION_SCHEMA.COLUMNS
 WHERE TABLE_NAME='engineers' AND COLUMN_NAME IN ('login','password_hash','center')
 AND TABLE_SCHEMA NOT IN ('svyazi_net_users','mysql','information_schema','performance_schema','sys')
 GROUP BY TABLE_SCHEMA HAVING COUNT(DISTINCT COLUMN_NAME)=3")
source_incidents=$(root_sql -e "SELECT TABLE_SCHEMA FROM INFORMATION_SCHEMA.COLUMNS
 WHERE TABLE_NAME='incidents' AND COLUMN_NAME IN ('PW','NAPR','TEHC','seed_key')
 AND TABLE_SCHEMA NOT IN ('svyazi_net_incidents','mysql','information_schema','performance_schema','sys')
 GROUP BY TABLE_SCHEMA HAVING COUNT(DISTINCT COLUMN_NAME)=4")

for source in "$source_users" "$source_incidents"; do
    case "$source" in
        *[!a-zA-Z0-9_]* ) echo 'Несколько исходных баз или недопустимое имя. Требуется ручной перенос.' >&2; exit 1 ;;
    esac
done

renames=''
add_table() {
    source=$1 target=$2 table=$3
    [ -n "$source" ] || return 0
    exists=$(root_sql -e "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA='$source' AND TABLE_NAME='$table'")
    [ "$exists" = 1 ] || return 0
    occupied=$(root_sql -e "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA='$target' AND TABLE_NAME='$table'")
    [ "$occupied" = 0 ] || { echo "Целевая таблица $target.$table уже существует. Автоматическое объединение отключено." >&2; exit 1; }
    [ -z "$renames" ] || renames="$renames, "
    renames="$renames\`$source\`.\`$table\` TO \`$target\`.\`$table\`"
}

for table in engineers duty_schedule; do add_table "$source_users" svyazi_net_users "$table"; done
for table in incidents shift_reports solutions bootstrap_data; do add_table "$source_incidents" svyazi_net_incidents "$table"; done

if [ -n "$renames" ]; then
    root_sql -e "RENAME TABLE $renames"
    echo '[svyazi.net] Таблицы предыдущей установки перенесены; ID, пароли и график сохранены.'
fi
