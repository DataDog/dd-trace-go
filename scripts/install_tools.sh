#!/bin/bash

TOOLS_MOD="./internal/tools"
TOOLS_FILE="tools.go"

cd $TOOLS_MOD
go mod tidy

# Extract blank imports from tools.go
IMPORTS=$(awk -F '"' '/_ "/ {print $2}' "$TOOLS_FILE")

# Install each extracted tool
for pkg in $IMPORTS; do
    echo "Installing $pkg"
    go install "$pkg"
done
