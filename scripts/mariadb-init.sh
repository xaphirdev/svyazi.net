#!/bin/sh
set -eu
# MYSQL_PASSWORD is supplied by Compose; encode it instead of interpolating SQL.
password_hex=$(printf %s "$MYSQL_PASSWORD" | od -An -tx1 | tr -d ' \n')
mysql -uroot -p"$MYSQL_ROOT_PASSWORD" <<SQL
CREATE DATABASE IF NOT EXISTS svyazi_net_users CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS svyazi_net_incidents CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
SET @password = CONVERT(0x$password_hex USING utf8mb4);
SET @statement = CONCAT('GRANT ALL PRIVILEGES ON svyazi_net_users.* TO ''svyazi_net''@''%'' IDENTIFIED BY ', QUOTE(@password));
PREPARE stmt FROM @statement;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
GRANT ALL PRIVILEGES ON svyazi_net_incidents.* TO 'svyazi_net'@'%';
SQL

if [ -f /opt/svyazi.net/migrate-databases.sh ]; then
    sh /opt/svyazi.net/migrate-databases.sh
fi
