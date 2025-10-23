#!/usr/bin/env bash
# Script to update version in pkg/version/version.go
# Usage: ./scripts/update-version.sh [VERSION]
#   If VERSION is not provided, uses the latest git tag

set -e

# Path to version file
VERSION_FILE="pkg/version/version.go"

if [ ! -f "$VERSION_FILE" ]; then
    echo "Error: Version file not found at $VERSION_FILE"
    exit 1
fi

# Get version from argument or latest git tag
if [ -n "$1" ]; then
    VERSION="$1"
    # Remove 'v' prefix if present
    VERSION=${VERSION#v}
else
    # Get the latest git tag
    LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    
    if [ -z "$LATEST_TAG" ]; then
        echo "Error: No git tags found and no version provided"
        echo "Usage: $0 [VERSION]"
        exit 1
    fi
    
    # Remove the 'v' prefix if present
    VERSION=${LATEST_TAG#v}
fi

# Get current version from file
CURRENT_VERSION=$(grep 'const Version = ' "$VERSION_FILE" | sed 's/.*"\(.*\)"/\1/')

if [ "$CURRENT_VERSION" = "$VERSION" ]; then
    echo "Version is already up to date: $VERSION"
    exit 0
fi

# Update the version in the file
echo "Updating version from $CURRENT_VERSION to $VERSION"
sed -i "s/const Version = \".*\"/const Version = \"$VERSION\"/" "$VERSION_FILE"

echo "✅ Version updated successfully to $VERSION"
echo "Agent string will be: @classic-terra/oracle-go@v$VERSION"
