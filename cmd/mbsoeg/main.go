package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/joho/godotenv"
	qdrant "github.com/qdrant/go-client/qdrant"

	"mbsoeg/internal/embeddings"
	"mbsoeg/internal/storage"
	"mbsoeg/pkg/models"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: Error loading .env file: %v", err)
	}

	// Parse command line arguments
	serverMode := flag.NewFlagSet("server", flag.ExitOnError)
	cliMode := flag.NewFlagSet("cli", flag.ExitOnError)
	jsonFile := cliMode.String("file", "", "Path to MBS items JSON file")

	if len(os.Args) < 2 {
		log.Fatal("Expected 'server' or 'cli' subcommands")
	}

	switch os.Args[1] {
	case "server":
		serverMode.Parse(os.Args[2:])
		runServer()
	case "cli":
		cliMode.Parse(os.Args[2:])
		runCLI(*jsonFile)
	default:
		log.Fatal("Expected 'server' or 'cli' subcommands")
	}
}

func runServer() {
	// Store server start time
	serverStartTime := time.Now()

	cfg := models.Config{
		QdrantHost:   os.Getenv("QDRANT_HOST"),
		QdrantPort:   6334,
		NumWorkers:   4,
		APIKey:       os.Getenv("OPENAI_API_KEY"),
		ServerPort:   8080,
		ServerAPIKey: os.Getenv("SERVER_API_KEY"),
	}

	// Override defaults with environment variables if set
	if port := os.Getenv("QDRANT_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.QdrantPort = p
		}
	}
	if workers := os.Getenv("NUM_WORKERS"); workers != "" {
		if w, err := strconv.Atoi(workers); err == nil {
			cfg.NumWorkers = w
		}
	}
	if port := os.Getenv("SERVER_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.ServerPort = p
		}
	}

	log.Printf("Starting server with config: QdrantHost=%s, QdrantPort=%d, NumWorkers=%d, ServerPort=%d",
		cfg.QdrantHost, cfg.QdrantPort, cfg.NumWorkers, cfg.ServerPort)

	// Initialize services
	log.Printf("Initializing OpenAI embeddings service...")
	embeddingsSvc := embeddings.NewService(cfg.APIKey)
	if err := embeddingsSvc.ValidateAPIKey(); err != nil {
		log.Fatalf("Invalid OpenAI API key: %v", err)
	}
	log.Printf("OpenAI API key validated successfully")

	log.Printf("Connecting to Qdrant at %s:%d...", cfg.QdrantHost, cfg.QdrantPort)
	storageSvc, err := storage.NewService(cfg.QdrantHost, cfg.QdrantPort)
	if err != nil {
		log.Fatalf("Failed to initialize storage service: %v", err)
	}
	log.Printf("Connected to Qdrant successfully")

	// Initialize collection
	ctx := context.Background()
	log.Printf("Initializing Qdrant collection...")
	if err := storageSvc.InitializeCollection(ctx); err != nil {
		log.Fatalf("Failed to initialize collection: %v", err)
	}
	log.Printf("Qdrant collection initialized successfully")

	// Track last request time and processing status
	var lastRequestTime *time.Time
	var isProcessing bool
	var processingMu sync.Mutex

	// Create a new HTTP server
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.ServerPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add CORS headers
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// Handle health check endpoint
			if r.Method == "GET" && r.URL.Path == "/" {
				processingMu.Lock()
				status := struct {
					Status       string    `json:"status"`
					StartTime    time.Time `json:"start_time"`
					Uptime       string    `json:"uptime"`
					LastRequest  time.Time `json:"last_request,omitempty"`
					IsProcessing bool      `json:"is_processing"`
					Config       struct {
						QdrantHost string `json:"qdrant_host"`
						QdrantPort int    `json:"qdrant_port"`
						NumWorkers int    `json:"num_workers"`
						ServerPort int    `json:"server_port"`
					} `json:"config"`
				}{
					Status:       "up",
					StartTime:    serverStartTime,
					Uptime:       time.Since(serverStartTime).String(),
					IsProcessing: isProcessing,
					Config: struct {
						QdrantHost string `json:"qdrant_host"`
						QdrantPort int    `json:"qdrant_port"`
						NumWorkers int    `json:"num_workers"`
						ServerPort int    `json:"server_port"`
					}{
						QdrantHost: cfg.QdrantHost,
						QdrantPort: cfg.QdrantPort,
						NumWorkers: cfg.NumWorkers,
						ServerPort: cfg.ServerPort,
					},
				}
				processingMu.Unlock()

				// If there's an active request, include its timestamp
				if lastRequestTime != nil {
					status.LastRequest = *lastRequestTime
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(status)
				return
			}

			// Handle /process endpoint
			if r.Method == "POST" && r.URL.Path == "/process" {
				// Update processing status
				processingMu.Lock()
				isProcessing = true
				processingMu.Unlock()
				defer func() {
					processingMu.Lock()
					isProcessing = false
					processingMu.Unlock()
				}()

				// Update last request time
				now := time.Now()
				lastRequestTime = &now

				// Validate API key
				apiKey := r.Header.Get("X-API-Key")
				if apiKey != cfg.ServerAPIKey {
					log.Printf("Invalid API key received: %s", apiKey)
					http.Error(w, "Invalid API key", http.StatusUnauthorized)
					return
				}
				log.Printf("API key validated successfully")

				// Parse request body
				log.Printf("Starting to parse request body...")
				var request struct {
					MBS_Items []models.MBSItem `json:"MBS_Items"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					log.Printf("Error parsing request body: %v", err)
					http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
					return
				}
				log.Printf("Successfully parsed request body with %d items", len(request.MBS_Items))

				// Process items
				var skippedCount, updatedCount int
				var mu sync.Mutex
				currentItems := make(map[string]bool)

				// Get existing points from Qdrant
				log.Printf("Getting existing points from Qdrant...")
				existingPoints, err := storageSvc.ScrollPoints(ctx, "descriptions")
				if err != nil {
					log.Printf("Failed to get existing points: %v", err)
					http.Error(w, fmt.Sprintf("Failed to get existing points: %v", err), http.StatusInternalServerError)
					return
				}
				log.Printf("Got %d existing points from Qdrant", len(existingPoints))

				// Create worker pool
				jobs := make(chan models.EmbeddingJob, len(request.MBS_Items))
				resultsChan := make(chan models.EmbeddingResult, len(request.MBS_Items))

				// Start workers
				var wg sync.WaitGroup
				for w := 1; w <= cfg.NumWorkers; w++ {
					wg.Add(1)
					go func(workerID int) {
						defer wg.Done()
						for job := range jobs {
							log.Printf("Worker %d processing item %s", workerID, job.ItemNum)
							vector, err := embeddingsSvc.GetEmbedding(fmt.Sprintf("MBS Item %s: %s", job.ItemNum, job.Item.Description))
							resultsChan <- models.EmbeddingResult{
								ItemNum: job.ItemNum,
								Vector:  vector,
								Item:    job.Item,
								NewHash: job.NewHash,
								Error:   err,
							}
						}
					}(w)
				}

				// Queue jobs for items that need processing
				jobCount := 0
				for i, item := range request.MBS_Items {
					log.Printf("Checking item %d/%d: %s", i+1, len(request.MBS_Items), item.ItemNum)
					currentItems[item.ItemNum] = true

					// Check if item needs updating
					descHash := storageSvc.GenerateHash(item)
					point, err := storageSvc.GetPoint(ctx, item.ItemNum, "descriptions")
					if err != nil {
						log.Printf("Error getting point for item %s: %v", item.ItemNum, err)
						continue
					}

					if point != nil {
						payload := point.Payload
						if hashValue, ok := payload["_hash"]; ok {
							if hash, ok := hashValue.GetKind().(*qdrant.Value_StringValue); ok {
								if hash.StringValue == descHash {
									log.Printf("Skipping unchanged item %s (hash: %s)", item.ItemNum, descHash)
									mu.Lock()
									skippedCount++
									mu.Unlock()
									continue
								}
								log.Printf("Item %s has changed (old hash: %s, new hash: %s)", item.ItemNum, hash.StringValue, descHash)
							}
						}
					} else {
						log.Printf("Item %s is new (hash: %s)", item.ItemNum, descHash)
					}

					jobs <- models.EmbeddingJob{
						ItemNum: item.ItemNum,
						Text:    fmt.Sprintf("MBS Item %s: %s", item.ItemNum, item.Description),
						Item:    item,
						NewHash: descHash,
					}
					jobCount++
				}
				close(jobs)
				log.Printf("Queued %d items for processing", jobCount)

				// Process results
				go func() {
					for result := range resultsChan {
						if result.Error != nil {
							log.Printf("Error processing item %s: %v", result.ItemNum, result.Error)
							continue
						}

						// Store in Qdrant
						log.Printf("Storing item %s in Qdrant...", result.ItemNum)
						payload := map[string]interface{}{
							// Metadata fields
							"_hash":       result.NewHash,
							"_last_check": time.Now().Format(time.RFC3339),

							// Required fields
							"item_num":               result.Item.ItemNum,
							"description":            result.Item.Description,
							"new_item":               result.Item.NewItem,
							"item_change":            result.Item.ItemChange,
							"fee_change":             result.Item.FeeChange,
							"benefit_change":         result.Item.BenefitChange,
							"anaes_change":           result.Item.AnaesChange,
							"emsn_change":            result.Item.EMSNChange,
							"descriptor_change":      result.Item.DescriptorChange,
							"anaes":                  result.Item.Anaes,
							"item_start_date":        result.Item.ItemStartDate,
							"item_end_date":          result.Item.ItemEndDate,
							"fee_start_date":         result.Item.FeeStartDate,
							"benefit_start_date":     result.Item.BenefitStartDate,
							"description_start_date": result.Item.DescriptionStartDate,
							"emsn_start_date":        result.Item.EMSNStartDate,
							"emsn_end_date":          result.Item.EMSNEndDate,
							"qfe_start_date":         result.Item.QFEStartDate,
							"qfe_end_date":           result.Item.QFEEndDate,
							"derived_fee_start_date": result.Item.DerivedFeeStartDate,
							"emsn_change_date":       result.Item.EMSNChangeDate,
							"schedule_fee":           result.Item.ScheduleFee,
							"derived_fee":            result.Item.DerivedFee,
							"benefit_75":             result.Item.Benefit75,
							"benefit_85":             result.Item.Benefit85,
							"benefit_100":            result.Item.Benefit100,
							"emsn_percentage_cap":    result.Item.EMSNPercentageCap,
							"emsn_maximum_cap":       result.Item.EMSNMaximumCap,
							"emsn_fixed_cap_amount":  result.Item.EMSNFixedCapAmount,
							"emsn_cap":               result.Item.EMSNCap,
							"basic_units":            result.Item.BasicUnits,
							"category":               result.Item.Category,
							"group":                  result.Item.Group,
							"sub_group":              result.Item.SubGroup,
							"sub_heading":            result.Item.SubHeading,
							"item_type":              result.Item.ItemType,
							"sub_item_num":           result.Item.SubItemNum,
							"benefit_type":           result.Item.BenefitType,
							"fee_type":               result.Item.FeeType,
							"provider_type":          result.Item.ProviderType,
							"emsn_description":       result.Item.EMSNDescription,
						}
						if err := storageSvc.UpsertPoint(ctx, result.ItemNum, result.Vector, payload, "descriptions"); err != nil {
							log.Printf("Error upserting point for item %s: %v", result.ItemNum, err)
							continue
						}

						mu.Lock()
						updatedCount++
						mu.Unlock()
					}
				}()

				// Wait for all workers to finish
				wg.Wait()
				close(resultsChan)

				// Remove items that no longer exist
				var removedCount int
				for _, point := range existingPoints {
					itemNum := fmt.Sprintf("%d", point.Id.GetNum())
					if !currentItems[itemNum] {
						if err := storageSvc.DeletePoint(ctx, itemNum, "descriptions"); err != nil {
							log.Printf("Error deleting point for item %s: %v", itemNum, err)
							continue
						}
						removedCount++
					}
				}

				// Print summary
				log.Printf("Processing complete:")
				log.Printf("- Items processed: %d", len(request.MBS_Items))
				log.Printf("- Items skipped (unchanged): %d", skippedCount)
				log.Printf("- Items updated: %d", updatedCount)
				log.Printf("- Items removed: %d", removedCount)

				// Return response
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status":        "success",
					"total_items":   len(request.MBS_Items),
					"skipped_items": skippedCount,
					"updated_items": updatedCount,
					"removed_items": removedCount,
				})
				log.Printf("Request completed successfully")
				return
			}

			// Handle unknown endpoints
			http.Error(w, "Not found", http.StatusNotFound)
		}),
	}

	// Start the server
	log.Printf("Starting server on port %d...", cfg.ServerPort)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func runCLI(jsonFile string) {
	if jsonFile == "" {
		log.Fatal("Please provide a path to the MBS items JSON file using the -file flag")
	}

	// Read and parse JSON file
	data, err := os.ReadFile(jsonFile)
	if err != nil {
		log.Fatalf("Error reading JSON file: %v", err)
	}

	var requestBody struct {
		MBSItems []models.MBSItem `json:"MBS_Items"`
	}
	if err := json.Unmarshal(data, &requestBody); err != nil {
		log.Fatalf("Error parsing JSON: %v", err)
	}

	items := requestBody.MBSItems
	if len(items) == 0 {
		log.Fatal("No MBS items found in the file")
	}

	// Initialize services
	cfg := models.Config{
		QdrantHost:   os.Getenv("QDRANT_HOST"),
		QdrantPort:   6334,
		NumWorkers:   4,
		APIKey:       os.Getenv("OPENAI_API_KEY"),
		ServerPort:   8080,
		ServerAPIKey: os.Getenv("SERVER_API_KEY"),
	}

	// Override defaults with environment variables if set
	if port := os.Getenv("QDRANT_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.QdrantPort = p
		}
	}
	if workers := os.Getenv("NUM_WORKERS"); workers != "" {
		if w, err := strconv.Atoi(workers); err == nil {
			cfg.NumWorkers = w
		}
	}
	if port := os.Getenv("SERVER_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.ServerPort = p
		}
	}

	// Validate OpenAI API key
	embeddingsSvc := embeddings.NewService(cfg.APIKey)
	if err := embeddingsSvc.ValidateAPIKey(); err != nil {
		log.Fatalf("Invalid OpenAI API key: %v", err)
	}

	// Initialize storage service
	storageSvc, err := storage.NewService(cfg.QdrantHost, cfg.QdrantPort)
	if err != nil {
		log.Fatalf("Failed to initialize storage service: %v", err)
	}

	// Initialize collection
	ctx := context.Background()
	if err := storageSvc.InitializeCollection(ctx); err != nil {
		log.Fatalf("Failed to initialize collection: %v", err)
	}

	// Process items
	var skippedCount, updatedCount int
	var mu sync.Mutex
	currentItems := make(map[string]bool)

	// Create channels for the worker pool
	jobs := make(chan models.EmbeddingJob, len(items))
	results := make(chan models.EmbeddingResult, len(items))

	// Start workers
	var wg sync.WaitGroup
	for w := 1; w <= cfg.NumWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				if cfg.NumWorkers > 1 {
					log.Printf("Worker %d processing item %s", workerID, job.ItemNum)
				}
				vector, err := embeddingsSvc.GetEmbedding(fmt.Sprintf("MBS Item %s: %s", job.ItemNum, job.Item.Description))
				results <- models.EmbeddingResult{
					ItemNum: job.ItemNum,
					Vector:  vector,
					Item:    job.Item,
					NewHash: job.NewHash,
					Error:   err,
				}
			}
		}(w)
	}

	// Process existing items
	existingPoints, err := storageSvc.ScrollPoints(ctx, "descriptions")
	if err != nil {
		log.Fatalf("Failed to get existing points: %v", err)
	}

	existingItems := make(map[string]bool)
	for _, point := range existingPoints {
		itemNum := fmt.Sprintf("%d", point.Id.GetNum())
		existingItems[itemNum] = true
	}

	// Queue jobs for items that need processing
	jobCount := 0
	for _, item := range items {
		currentItems[item.ItemNum] = true
		descHash := storageSvc.GenerateHash(item)

		// Get existing point to check hash
		point, err := storageSvc.GetPoint(ctx, item.ItemNum, "descriptions")
		if err != nil {
			log.Printf("Error getting point for item %s: %v", item.ItemNum, err)
			continue
		}

		if point != nil {
			payload := point.Payload
			if hashValue, ok := payload["_hash"]; ok {
				if hash, ok := hashValue.GetKind().(*qdrant.Value_StringValue); ok {
					if hash.StringValue == descHash {
						mu.Lock()
						skippedCount++
						mu.Unlock()
						continue
					}
					log.Printf("Item %s has changed (old hash: %s, new hash: %s)", item.ItemNum, hash.StringValue, descHash)
				}
			}
		} else {
			log.Printf("Item %s is new (hash: %s)", item.ItemNum, descHash)
		}

		jobs <- models.EmbeddingJob{
			ItemNum: item.ItemNum,
			Text:    fmt.Sprintf("MBS Item %s: %s", item.ItemNum, item.Description),
			Item:    item,
			NewHash: descHash,
		}
		jobCount++
	}
	close(jobs)

	// Process results
	for i := 0; i < jobCount; i++ {
		result := <-results
		if result.Error != nil {
			log.Printf("Error processing item %s: %v", result.ItemNum, result.Error)
			continue
		}

		// Create a map of individual fields for the payload
		payload := map[string]interface{}{
			// Metadata fields
			"_hash":       result.NewHash,
			"_last_check": time.Now().Format(time.RFC3339),

			// Required fields
			"item_num":    result.Item.ItemNum,
			"description": result.Item.Description,

			// Boolean fields
			"new_item":          result.Item.NewItem,
			"item_change":       result.Item.ItemChange,
			"fee_change":        result.Item.FeeChange,
			"benefit_change":    result.Item.BenefitChange,
			"anaes_change":      result.Item.AnaesChange,
			"emsn_change":       result.Item.EMSNChange,
			"descriptor_change": result.Item.DescriptorChange,
			"anaes":             result.Item.Anaes,

			// Date fields
			"item_start_date":        result.Item.ItemStartDate,
			"item_end_date":          result.Item.ItemEndDate,
			"fee_start_date":         result.Item.FeeStartDate,
			"benefit_start_date":     result.Item.BenefitStartDate,
			"description_start_date": result.Item.DescriptionStartDate,
			"emsn_start_date":        result.Item.EMSNStartDate,
			"emsn_end_date":          result.Item.EMSNEndDate,
			"qfe_start_date":         result.Item.QFEStartDate,
			"qfe_end_date":           result.Item.QFEEndDate,
			"derived_fee_start_date": result.Item.DerivedFeeStartDate,
			"emsn_change_date":       result.Item.EMSNChangeDate,

			// Float/numeric fields
			"schedule_fee":          result.Item.ScheduleFee,
			"derived_fee":           result.Item.DerivedFee,
			"benefit_75":            result.Item.Benefit75,
			"benefit_85":            result.Item.Benefit85,
			"benefit_100":           result.Item.Benefit100,
			"emsn_percentage_cap":   result.Item.EMSNPercentageCap,
			"emsn_maximum_cap":      result.Item.EMSNMaximumCap,
			"emsn_fixed_cap_amount": result.Item.EMSNFixedCapAmount,
			"emsn_cap":              result.Item.EMSNCap,
			"basic_units":           result.Item.BasicUnits,

			// String fields
			"category":         result.Item.Category,
			"group":            result.Item.Group,
			"sub_group":        result.Item.SubGroup,
			"sub_heading":      result.Item.SubHeading,
			"item_type":        result.Item.ItemType,
			"sub_item_num":     result.Item.SubItemNum,
			"benefit_type":     result.Item.BenefitType,
			"fee_type":         result.Item.FeeType,
			"provider_type":    result.Item.ProviderType,
			"emsn_description": result.Item.EMSNDescription,
		}
		if err := storageSvc.UpsertPoint(ctx, result.ItemNum, result.Vector, payload, "descriptions"); err != nil {
			log.Printf("Error upserting point for item %s: %v", result.ItemNum, err)
			continue
		}

		mu.Lock()
		updatedCount++
		mu.Unlock()
	}

	// Wait for all workers to finish
	wg.Wait()
	close(results)

	// Remove items that no longer exist
	var removedCount int
	for itemNum := range existingItems {
		if !currentItems[itemNum] {
			if err := storageSvc.DeletePoint(ctx, itemNum, "descriptions"); err != nil {
				log.Printf("Error deleting point for item %s: %v", itemNum, err)
				continue
			}
			removedCount++
		}
	}

	// Print summary
	log.Printf("Processing complete:")
	log.Printf("- Items processed: %d", len(items))
	log.Printf("- Items skipped (unchanged): %d", skippedCount)
	log.Printf("- Items updated: %d", updatedCount)
	log.Printf("- Items removed: %d", removedCount)
}
