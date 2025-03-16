# Migration Guide

This guide explains how to migrate data between different versions of the MBS Code Embeddings Generator and how to manage your Qdrant vector database data.

## Data Migration

### Backing Up Qdrant Data

To create a backup of your Qdrant data:

```bash
# Using Docker Compose
docker-compose -f deployments/docker-compose.yml exec qdrant \
  tar czf /qdrant/storage/backup.tar.gz /qdrant/storage

# Copy backup to host
docker cp $(docker-compose -f deployments/docker-compose.yml ps -q qdrant):/qdrant/storage/backup.tar.gz ./backup.tar.gz
```

### Restoring Qdrant Data

1. Stop the services:
```bash
docker-compose -f deployments/docker-compose.yml down
```

2. Replace the storage directory with your backup:
```bash
rm -rf ./qdrant_data/*
tar xzf backup.tar.gz -C ./qdrant_data
```

3. Restart the services:
```bash
docker-compose -f deployments/docker-compose.yml up -d
```
