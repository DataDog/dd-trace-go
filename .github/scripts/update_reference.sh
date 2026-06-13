#!/bin/bash

# This script updates the reference in a YAML file.

# Check if required environment variables are set
if [ -z "$TARGET" ]; then
    echo "Error: TARGET environment variable is not set"
    exit 1
fi

if [ -z "$REF" ]; then
    echo "Error: REF environment variable is not set"
    exit 1
fi

if [ -z "$PATTERN" ]; then
    echo "Error: PATTERN environment variable is not set"
    exit 1
fi

echo "Target: $TARGET"
echo "Ref: $REF"

# Remove leading and trailing forward slashes from pattern
CLEAN_PATTERN=$(echo "$PATTERN" | sed 's/^\///;s/\/$//')
echo "Pattern: $CLEAN_PATTERN"

# Create a temporary file
TEMP_FILE=$(mktemp)

# Set trap to cleanup on script exit
trap 'rm -f "$TEMP_FILE"' EXIT

# Read the file and perform the substitution
if [ -f "$TARGET" ]; then
    # Perform the substitution and save to temporary file
    # We use perl here because sed's regex support varies across platforms
    perl -pe "s/$CLEAN_PATTERN/\${1}$REF\${3}/g" "$TARGET" > "$TEMP_FILE"

    # Compare files to check if any changes were made
    if cmp -s "$TARGET" "$TEMP_FILE"; then
        echo "No references found in $TARGET"
    else
        # Copy the temp file back to the target
        cp "$TEMP_FILE" "$TARGET"
        echo "✓ Updated references in $TARGET"
    fi
else
    echo "Error: Target file $TARGET does not exist"
    exit 1
fi