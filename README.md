# MBS Code Embeddings Generator

This tool generates and maintains semantic embeddings for Medicare Benefits Schedule (MBS) items using OpenAI's embedding API and stores them in Qdrant vector database. It efficiently handles updates by only processing changed items.

## Project Structure

```
.
├── cmd/
│   └── mbsoeg/          # Main application entry point
├── internal/
│   ├── embeddings/      # OpenAI embeddings service
│   └── storage/         # Qdrant storage service
├── pkg/
│   └── models/          # Shared data models
├── deployments/         # Deployment configuration
│   ├── Dockerfile
│   └── docker-compose.yml
└── ...
```

## Features

- Generates semantic embeddings for MBS item descriptions
- Stores embeddings and metadata in Qdrant vector database
- Efficient update mechanism that only processes changed items
- Automatic cleanup of removed items
- State tracking using Qdrant's payload system
- Parallel processing with configurable worker count
- Early validation of OpenAI API key
- Comprehensive error handling and logging
- Supports both CLI and Server modes
- RESTful API with API key authentication

## Prerequisites

- Go 1.22 or later
- Docker and Docker Compose
- OpenAI API key with access to text-embedding-ada-002 model
- MBS items JSON file (in the specified format)
- At least 2GB of available RAM
- Stable internet connection for API calls

## Setup

1. Clone the repository:
```bash
git clone <repository-url>
cd mbsoeg
```

2. Create a `.env` file in the project root (copy from `.env.example`):
```bash
cp .env.example .env
```

3. Configure the following environment variables in `.env`:
```bash
# OpenAI API Configuration (required)
OPENAI_API_KEY=your_openai_api_key  # API key for OpenAI's embedding service

# Server Configuration
SERVER_PORT=8080                     # Port for the HTTP server (default: 8080)
SERVER_API_KEY=your_server_api_key   # API key for authenticating requests

# Processing Configuration
NUM_WORKERS=1                        # Number of parallel workers for processing embeddings (default: 1)

# Qdrant Configuration (when running without Docker)
QDRANT_HOST=localhost               # Qdrant server hostname (default: localhost, in Docker: qdrant)
QDRANT_PORT=6334                    # Qdrant gRPC port (default: 6334)
```

## Usage

The tool supports two modes of operation: CLI mode and Server mode.

### CLI Mode

Run the tool directly with a JSON file:

```bash
# Build the binary
go build -o mbsoeg ./cmd/mbsoeg

# Run in CLI mode
./mbsoeg cli -file path/to/mbs_items.json
```

The CLI mode will:
1. Load and validate the MBS items from the JSON file
2. Connect to Qdrant (using environment variables)
3. Process all items in parallel
4. Print progress and summary information
5. Exit when complete

### Server Mode

1. Start the services using Docker Compose:
```bash
docker-compose -f deployments/docker-compose.yml --env-file .env up -d
```

2. Send MBS items data to the API:
```bash
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your_server_api_key" \
  -d @mbs_items.json
```

For PowerShell users:
```powershell
curl -X POST `
  http://localhost:8080/process `
  -H "Content-Type: application/json" `
  -H "X-API-Key: your_server_api_key" `
  -d "@path\to\mbs_items.json"
```

The server will:
1. Validate the API key in the request header
2. Process the MBS items from the request body
3. Generate embeddings for new or modified items
4. Remove items that no longer exist
5. Return a summary of the processing results

### API Response Format

The API returns a JSON response with processing statistics:
```json
{
  "items_processed": 1000,  // Total number of items in the request
  "items_skipped": 950,     // Items that haven't changed (same hash)
  "items_updated": 45,      // Items that were added or updated
  "items_removed": 5        // Items that were removed (not in new data)
}
```

### Input JSON Format

The input JSON file must follow this structure:

```json
{
  "MBS_Items": [
    {
      "ItemNum": "104",
      "Description": "Professional attendance at consulting rooms...",
      "Anaes": false,
      "AnaesChange": false,
      "BasicUnits": 0,
      "Benefit100": 89.55,
      "Benefit75": 67.15,
      "Benefit85": 76.15,
      "BenefitStartDate": "2020-07-01",
      "BenefitType": "75",
      "Category": "1",
      "Group": "A1",
      "ScheduleFee": 89.55
      // ... other fields are optional
    }
  ]
}
```

Required fields:
- `ItemNum`: MBS item number (string)
- `Description`: Item description (string)
- Other fields are optional but will be stored if provided

## Error Handling

Common errors and solutions:

1. "Invalid API key":
   - Check that your SERVER_API_KEY in .env matches the one in your request headers
   - Verify the X-API-Key header is correctly set in your request
   - Make sure there are no extra spaces or newlines in the key

2. "Error parsing request body":
   - Ensure your JSON follows the correct format with "MBS_Items" array
   - Validate your JSON structure and syntax
   - Check that the file is properly encoded (UTF-8)

3. "No MBS items found in request":
   - Check that your JSON contains items in the MBS_Items array
   - Verify the array is not empty
   - Ensure the array is under the "MBS_Items" key

4. "Failed to connect to Qdrant":
   - Ensure the Qdrant service is running (`docker-compose ps`)
   - Check Docker Compose logs (`docker-compose logs qdrant`)
   - Verify network connectivity between services
   - Check if Qdrant ports (6333, 6334) are accessible

5. OpenAI API errors:
   - Verify your OpenAI API key is valid and has sufficient credits
   - Check for rate limiting or quota issues
   - Ensure network connectivity to OpenAI's API

## Docker Compose Services

The `docker-compose.yml` file defines two services:

1. `mbsoeg`: The main application service
   - Runs in server mode
   - Exposes the configured port (default: 8080)
   - Connects to Qdrant service
   - Uses environment variables from .env file
   - Automatically restarts on failure

2. `qdrant`: Vector database service
   - Persistent storage for embeddings
   - Exposes ports 6333 (HTTP) and 6334 (gRPC)
   - Data persisted in a named volume
   - No authentication required (internal network only)

## Environment Variables

The application uses the following environment variables:

```bash
# OpenAI API Configuration (required)
OPENAI_API_KEY=your_openai_api_key  # API key for OpenAI's embedding service

# Server Configuration
SERVER_PORT=8080                     # Port for the HTTP server (default: 8080)
SERVER_API_KEY=your_server_api_key   # API key for authenticating requests

# Processing Configuration
NUM_WORKERS=4                        # Number of parallel workers for processing embeddings (default: 4)

# Qdrant Configuration (when running without Docker)
QDRANT_HOST=localhost               # Qdrant server hostname (default: localhost, in Docker: qdrant)
QDRANT_PORT=6334                    # Qdrant gRPC port (default: 6334)
```