#!/usr/bin/env bash
# Script to create a new release with proper version management
# Usage: ./scripts/release.sh VERSION
#   Example: ./scripts/release.sh 1.0.2

set -e

if [ -z "$1" ]; then
    echo "Error: Version required"
    echo "Usage: $0 VERSION"
    echo "Example: $0 1.0.2"
    exit 1
fi

VERSION="$1"
# Remove 'v' prefix if present for version file
VERSION_NUMBER=${VERSION#v}
# Ensure tag has 'v' prefix
TAG="v${VERSION_NUMBER}"

# Check if we're in a git repository
if [ ! -d ".git" ]; then
    echo "Error: Not in a git repository"
    exit 1
fi

if ! make ci ; then
	echo "Error: CI check failed"
	exit 1
fi

# Check if working directory is clean
if ! git diff-index --quiet HEAD --; then
    echo "Error: Working directory is not clean. Commit or stash your changes first."
    exit 1
fi

# Check if tag already exists
if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "Error: Tag $TAG already exists"
    exit 1
fi

echo "Creating release $TAG..."
echo ""

# Step 1: Update version file
echo "→ Updating version file to $VERSION_NUMBER"
./scripts/update-version.sh "$VERSION_NUMBER"

# Step 2: Commit the version change
echo "→ Committing version change"
git add pkg/version/version.go
git commit -m "chore: bump version to $VERSION_NUMBER"

# Step 3: Create the tag
echo "→ Creating git tag $TAG"
git tag -a "$TAG" -m "Release $TAG"

echo ""
echo "✅ Release $TAG created successfully!"
echo ""
echo "Next steps:"
echo "  1. Review the changes: git show HEAD"
echo "  2. Push the commit: git push"
echo "  3. Push the tag: git push origin $TAG"
echo ""
echo "Or push both at once: git push && git push origin $TAG"
