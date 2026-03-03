#!/bin/sh
set -eu

# ==============================================
# Unified Entrypoint for Multi-Cloud Deployment
# Supports: AWS Lambda, AWS Fargate, GCP Cloud Run, Azure Container Apps
# ==============================================

echo "🚀 CUDly starting..."
echo "   Environment: ${ENVIRONMENT:-production}"
echo "   Runtime Mode: ${RUNTIME_MODE:-auto}"

# Auto-detect runtime environment if set to 'auto'
if [ "$RUNTIME_MODE" = "auto" ]; then
  if [ -n "$AWS_LAMBDA_RUNTIME_API" ]; then
    echo "   Detected: AWS Lambda"
    RUNTIME_MODE=lambda
  elif [ -n "$K_SERVICE" ]; then
    echo "   Detected: GCP Cloud Run"
    RUNTIME_MODE=http
  elif [ -n "$CONTAINER_APP_NAME" ]; then
    echo "   Detected: Azure Container Apps"
    RUNTIME_MODE=http
  elif [ -n "$ECS_CONTAINER_METADATA_URI" ]; then
    echo "   Detected: AWS ECS Fargate"
    RUNTIME_MODE=http
  else
    echo "   Detected: Standard container (defaulting to HTTP mode)"
    RUNTIME_MODE=http
  fi
fi

echo "   Starting in: $RUNTIME_MODE mode"

# ==============================================
# Database Migrations
# ==============================================

if [ "$DB_AUTO_MIGRATE" = "true" ]; then
  echo "📦 Running database migrations..."

  # Build database connection string
  DB_HOST=${DB_HOST:-localhost}
  DB_PORT=${DB_PORT:-5432}
  DB_NAME=${DB_NAME:-cudly}
  DB_USER=${DB_USER:-cudly}
  DB_SSL_MODE=${DB_SSL_MODE:-require}

  # Get database password from secret or environment
  if [ -n "$DB_PASSWORD_SECRET" ] && [ "$SECRET_PROVIDER" != "env" ]; then
    echo "   Resolving DB password from secret manager..."
    # Password will be resolved by the application
    # Migration will use the environment variable if available
    if [ -z "$DB_PASSWORD" ]; then
      echo "   ⚠️  DB_PASSWORD not set, migrations may fail"
      echo "   Application will attempt to resolve from secret manager on startup"
    fi
  fi

  if [ -n "$DB_PASSWORD" ]; then
    # Run migrations if password is available
    # URL-encode password to handle special characters (generated passwords contain =,[,$, etc.)
    ENCODED_PASSWORD=$(printf '%s' "$DB_PASSWORD" | awk 'BEGIN{split("",hex); for(i=0;i<256;i++){c=sprintf("%c",i); hex[c]=sprintf("%%%02X",i)}} {n=length($0); for(i=1;i<=n;i++){c=substr($0,i,1); if(c~/[A-Za-z0-9._~-]/)printf "%s",c; else printf "%s",hex[c]}}')
    DB_URL="postgresql://${DB_USER}:${ENCODED_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSL_MODE}"

    if migrate -path "$DB_MIGRATIONS_PATH" -database "$DB_URL" up; then
      echo "   ✅ Migrations completed successfully"
    else
      MIGRATE_EXIT_CODE=$?
      if [ $MIGRATE_EXIT_CODE -eq 1 ]; then
        # Exit code 1 means "no change", which is okay
        echo "   ℹ️  No new migrations to apply"
      else
        echo "   ❌ Migration failed with exit code $MIGRATE_EXIT_CODE"
        exit $MIGRATE_EXIT_CODE
      fi
    fi
  else
    echo "   ⚠️  Skipping migrations (DB_PASSWORD not available)"
    echo "   Application will handle migrations on first connection"
  fi
else
  echo "📦 Skipping database migrations (DB_AUTO_MIGRATE=false)"
fi

# ==============================================
# Start Application
# ==============================================

case $RUNTIME_MODE in
  lambda)
    echo "🔷 Starting AWS Lambda handler..."
    echo "   Lambda Runtime API: $AWS_LAMBDA_RUNTIME_API"
    echo "   Handler: /app/cudly --mode=lambda"

    # Lambda mode: Use AWS Lambda Runtime Interface
    exec "$@" --mode=lambda
    ;;

  http)
    echo "🌐 Starting HTTP server..."
    echo "   Port: ${PORT:-8080}"
    echo "   Handler: /app/cudly --mode=http"

    # HTTP mode: Standard HTTP server for Fargate, Cloud Run, Container Apps
    exec "$@" --mode=http --port="${PORT:-8080}"
    ;;

  *)
    echo "❌ Unknown RUNTIME_MODE: $RUNTIME_MODE"
    echo "   Valid modes: lambda, http, auto"
    exit 1
    ;;
esac
