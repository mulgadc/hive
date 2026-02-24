#!/bin/bash

# Hive Development Dependencies Setup Script
# This script clones Viperblock and Predastore repositories for cross-repo development

# TODO: Improve, remove CLAUDE readme reference, simplify next-steps
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

echo "🏗️  Setting up Hive development environment..."
echo "Project root: $PROJECT_ROOT"
echo "Mulga root: $MULGA_ROOT"

# Check if we're in the correct directory structure
if [[ ! -f "$PROJECT_ROOT/go.mod" ]]; then
    echo "❌ Error: Cannot find go.mod. Please run this script from the hive repository."
    exit 1
fi

# Function to clone or update repository
clone_or_update() {
    local repo_url="$1"
    local repo_name="$2"
    local target_dir="$MULGA_ROOT/$repo_name"

    if [[ -d "$target_dir" ]]; then
        echo "📂 $repo_name already exists at $target_dir"
        echo "   To update, run: cd $target_dir && git pull"
    else
        echo "📥 Cloning $repo_name to $target_dir..."
        git clone "$repo_url" "$target_dir"
        echo "✅ Successfully cloned $repo_name"
    fi
}

# Repository URLs - Update these with actual repository URLs
VIPERBLOCK_REPO="${VIPERBLOCK_REPO:-https://github.com/mulgadc/viperblock.git}"
PREDASTORE_REPO="${PREDASTORE_REPO:-https://github.com/mulgadc/predastore.git}"

echo "🔗 Repository URLs:"
echo "   Viperblock: $VIPERBLOCK_REPO"
echo "   Predastore: $PREDASTORE_REPO"
echo ""

# Clone dependencies
clone_or_update "$VIPERBLOCK_REPO" "viperblock"
clone_or_update "$PREDASTORE_REPO" "predastore"

# Verify go.mod replace directives
echo ""
echo "🔍 Verifying go.mod replace directives..."

if grep -q "replace github.com/mulgadc/viperblock => ../viperblock" "$PROJECT_ROOT/go.mod"; then
    echo "✅ Viperblock replace directive found"
else
    echo "⚠️  Adding Viperblock replace directive to go.mod"
    echo "replace github.com/mulgadc/viperblock => ../viperblock" >> "$PROJECT_ROOT/go.mod"
fi

if grep -q "replace github.com/mulgadc/predastore => ../predastore" "$PROJECT_ROOT/go.mod"; then
    echo "✅ Predastore replace directive found"
else
    echo "⚠️  Adding Predastore replace directive to go.mod"
    echo "replace github.com/mulgadc/predastore => ../predastore" >> "$PROJECT_ROOT/go.mod"
fi

# Verify directory structure
echo ""
echo "📁 Directory structure:"
ls -la "$MULGA_ROOT" | grep -E "(hive|viperblock|predastore)" || true

echo ""
echo "🎉 Development environment setup complete!"
echo ""
echo "Next step:"
echo "Run development setup to build services: ./scripts/dev-setup.sh"
echo ""
