#!/bin/sh
# Перегенеровує contract/ui-manifest.json — перелік файлів спільного UI
# з їхніми sha256.
#
# Навіщо маніфест, а не просто список у скрипті синхронізації: список
# довелось би тримати в ІНШОМУ репозиторії, і новий модуль з'являвся б у
# ньому лише тоді, коли хтось згадає його дописати. Манiфест будується з
# самого каталогу, тож забути неможливо — а sha256 перетворює «здається,
# синхронізовано» на перевірюване твердження.
#
#   sh scripts/gen-ui-manifest.sh          — перезаписати манiфест
#   sh scripts/gen-ui-manifest.sh --check  — лише перевірити, що він свіжий
#
# Запускати з кореня репозиторію.
set -eu

WEB=internal/api/web
OUT=contract/ui-manifest.json

[ -d "$WEB" ] || { echo "запускати з кореня репозиторію oddinvest" >&2; exit 2; }

# Спільне — це js/ і css/. index.html не входить: він хост саме веб-версії,
# у панелі HA свій. package.json теж ні — він лише для `node --check`.
files=$(cd "$WEB" && find js css -type f \( -name '*.js' -o -name '*.css' \) | sort)

tmp=$(mktemp)
{
  echo '{'
  echo '  "_comment": "Згенеровано scripts/gen-ui-manifest.sh — руками не правити.",'
  echo '  "root": "internal/api/web",'
  echo '  "files": ['
  first=1
  for f in $files; do
    sum=$(sha256sum "$WEB/$f" | cut -d' ' -f1)
    [ $first -eq 1 ] || echo ','
    first=0
    printf '    { "path": "%s", "sha256": "%s" }' "$f" "$sum"
  done
  echo
  echo '  ]'
  echo '}'
} > "$tmp"

if [ "${1:-}" = "--check" ]; then
  if cmp -s "$tmp" "$OUT"; then
    echo "манiфест свіжий ($(echo "$files" | wc -l | tr -d ' ') файлів)"
    rm -f "$tmp"
  else
    echo "!! $OUT застарів — перегенеруй: sh scripts/gen-ui-manifest.sh" >&2
    diff -u "$OUT" "$tmp" || true
    rm -f "$tmp"
    exit 1
  fi
else
  mv "$tmp" "$OUT"
  echo "записано $OUT ($(echo "$files" | wc -l | tr -d ' ') файлів)"
fi
