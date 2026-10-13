@echo off
rem Ежедневный запуск: сбор выдачи по списку из queries.csv
cd /d "%~dp0"
go run ./cmd %*
