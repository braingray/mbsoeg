# MBS Code Embeddings Generator

This tool generates and maintains semantic embeddings for Medicare Benefits Schedule (MBS) items using OpenAI's embedding API and stores them in Qdrant vector database.

## Features

- Generates semantic embeddings for MBS item descriptions
- Stores embeddings and metadata in Qdrant vector database
- Efficient update mechanism that only processes changed items
- Supports both CLI and Server modes with parallel processing

## Prerequisites

- Go 1.22 or later
- Docker and Docker Compose
- OpenAI API key

## Setup

1. Clone the repository and create a `.env` file:
```bash
cp .env.example .env
```

2. Configure environment variables in `.env`:
```bash
# Required
OPENAI_API_KEY=your_openai_api_key
SERVER_API_KEY=your_server_api_key

# Optional with defaults
SERVER_PORT=8080
NUM_WORKERS=4
QDRANT_HOST=qdrant  # Use 'localhost' when running without Docker
QDRANT_PORT=6334
```

## Usage

### CLI Mode

```bash
# Build and run
go build -o mbsoeg ./cmd/mbsoeg
./mbsoeg cli -file path/to/mbs_items.json
```

### Server Mode

1. Start services:
```bash
docker-compose -f deployments/docker-compose.yml --env-file .env up -d
```

2. Send data:
```bash
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your_server_api_key" \
  -d @mbs_items.json
```

3. Check status:
```bash
curl http://localhost:8080/
```

### Input JSON Format

```json
{
  "MBS_Items": [
    {
      "ItemNum": "104",
      "Description": "Professional attendance at consulting rooms...",
      "ScheduleFee": 89.55,
      "Benefit100": 89.55,
      "Category": "1",
      "BenefitType": "75"
      // Other fields optional
    }
  ]
}
```

### API Response Format

```json
{
  "status": "success",
  "total_items": 1000,
  "skipped_items": 950,
  "updated_items": 45,
  "removed_items": 5
}
```

## Troubleshooting

- **Invalid API key**: Verify X-API-Key header matches SERVER_API_KEY in .env
- **Connection issues**: Ensure Qdrant is running (`docker-compose ps`)
- **OpenAI errors**: Check API key validity and rate limits

## Health Check

The root endpoint (`/`) provides service status:
```json
{
  "status": "up",
  "start_time": "2025-03-17T02:23:28Z",
  "uptime": "25.37s",
  "is_processing": false,
  "config": {
    "qdrant_host": "qdrant",
    "qdrant_port": 6334,
    "num_workers": 4,
    "server_port": 8080
  }
}
```

## Docker Services

- **mbsoeg**: Main application (port 8080)
- **qdrant**: Vector database (ports 6333/HTTP, 6334/gRPC)