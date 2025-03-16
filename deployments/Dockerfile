FROM golang:1.22-alpine

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go.mod and go.sum first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o mbsoeg ./cmd/mbsoeg

# Use a minimal base image for the final container
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=0 /app/mbsoeg .

# Copy the example environment file
COPY .env.example .env

# Set up entrypoint and default command
ENTRYPOINT ["./mbsoeg"]
CMD ["server"] 