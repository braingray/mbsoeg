package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
	cfg := models.Config{
		QdrantHost:   os.Getenv("QDRANT_HOST"),
		QdrantPort:   6334,
		NumWorkers:   4,
		APIKey:       os.Getenv("OPENAI_API_KEY"),
		ServerPort:   8080,
		ServerAPIKey: os.Getenv("SERVER_API_KEY"),
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

	http.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request from %s", r.RemoteAddr)

		// Check API key
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != cfg.ServerAPIKey {
			log.Printf("Invalid API key received from %s", r.RemoteAddr)
			http.Error(w, "Invalid API key", http.StatusUnauthorized)
			return
		}
		log.Printf("API key validated successfully")

		// Parse request body
		log.Printf("Parsing request body...")
		var requestBody struct {
			MBSItems []models.MBSItem `json:"MBS_Items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			log.Printf("Error parsing request body: %v", err)
			http.Error(w, fmt.Sprintf("Error parsing request body: %v", err), http.StatusBadRequest)
			return
		}
		log.Printf("Request body parsed successfully")

		items := requestBody.MBSItems
		if len(items) == 0 {
			log.Printf("No MBS items found in request")
			http.Error(w, "No MBS items found in request", http.StatusBadRequest)
			return
		}
		log.Printf("Found %d MBS items in request", len(items))

		// Process items
		currentItems := make(map[string]bool)
		var skippedCount, updatedCount int
		var mu sync.Mutex

		// Create channels for the worker pool
		jobs := make(chan models.EmbeddingJob, len(items))
		results := make(chan models.EmbeddingResult, len(items))

		// Start workers
		log.Printf("Starting %d workers...", cfg.NumWorkers)
		var wg sync.WaitGroup
		for i := 1; i <= cfg.NumWorkers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				log.Printf("Worker %d started", workerID)
				for job := range jobs {
					log.Printf("Worker %d processing item %s", workerID, job.ItemNum)
					vector, err := embeddingsSvc.GetEmbedding(fmt.Sprintf("MBS Item %s: %s", job.ItemNum, job.Item.Description))
					if err != nil {
						log.Printf("Worker %d error getting embedding for item %s: %v", workerID, job.ItemNum, err)
					} else {
						log.Printf("Worker %d got embedding for item %s", workerID, job.ItemNum)
					}
					results <- models.EmbeddingResult{
						ItemNum: job.ItemNum,
						Vector:  vector,
						Item:    job.Item,
						NewHash: job.NewHash,
						Error:   err,
					}
				}
				log.Printf("Worker %d finished", workerID)
			}(i)
		}

		// Process existing items
		log.Printf("Getting existing points from Qdrant...")
		existingPoints, err := storageSvc.ScrollPoints(ctx, "descriptions")
		if err != nil {
			log.Printf("Failed to get existing points: %v", err)
			http.Error(w, fmt.Sprintf("Failed to get existing points: %v", err), http.StatusInternalServerError)
			return
		}
		log.Printf("Got %d existing points from Qdrant", len(existingPoints))

		existingItems := make(map[string]bool)
		for _, point := range existingPoints {
			itemNum := fmt.Sprintf("%d", point.Id.GetNum())
			existingItems[itemNum] = true
		}

		// Queue jobs for items that need processing
		log.Printf("Queueing jobs for processing...")
		jobCount := 0
		for _, item := range items {
			currentItems[item.ItemNum] = true
			descHash := storageSvc.GenerateHash(item)

			// Get existing point to check hash
			log.Printf("Checking existing point for item %s...", item.ItemNum)
			point, err := storageSvc.GetPoint(ctx, item.ItemNum, "descriptions")
			if err != nil {
				log.Printf("Error getting point for item %s: %v", item.ItemNum, err)
				continue
			}

			if point != nil {
				payload := point.Payload
				if hash, ok := payload["_hash"].GetKind().(*qdrant.Value_StringValue); ok && hash.StringValue == descHash {
					mu.Lock()
					skippedCount++
					mu.Unlock()
					log.Printf("Skipping unchanged item %s", item.ItemNum)
					continue
				}
			}

			log.Printf("Queueing item %s for processing", item.ItemNum)
			jobs <- models.EmbeddingJob{
				ItemNum: item.ItemNum,
				Text:    fmt.Sprintf("MBS Item %s: %s", item.ItemNum, item.Description),
				Item:    item,
				NewHash: descHash,
			}
			jobCount++
		}
		log.Printf("Queued %d jobs for processing", jobCount)
		close(jobs)

		// Process results
		log.Printf("Processing results...")
		for i := 0; i < jobCount; i++ {
			result := <-results
			if result.Error != nil {
				log.Printf("Error processing item %s: %v", result.ItemNum, result.Error)
				continue
			}

			log.Printf("Upserting point for item %s...", result.ItemNum)
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
			log.Printf("Successfully upserted point for item %s", result.ItemNum)

			mu.Lock()
			updatedCount++
			mu.Unlock()
		}

		// Wait for all workers to finish
		log.Printf("Waiting for workers to finish...")
		wg.Wait()
		close(results)
		log.Printf("All workers finished")

		// Remove items that no longer exist
		log.Printf("Removing obsolete items...")
		var removedCount int
		for itemNum := range existingItems {
			if !currentItems[itemNum] {
				log.Printf("Removing item %s...", itemNum)
				if err := storageSvc.DeletePoint(ctx, itemNum, "descriptions"); err != nil {
					log.Printf("Error deleting point for item %s: %v", itemNum, err)
					continue
				}
				log.Printf("Successfully removed item %s", itemNum)
				removedCount++
			}
		}

		// Send response
		response := map[string]interface{}{
			"items_processed": len(items),
			"items_skipped":   skippedCount,
			"items_updated":   updatedCount,
			"items_removed":   removedCount,
		}

		w.Header().Set("Content-Type", "application/json")
		log.Printf("Sending response: %+v", response)
		json.NewEncoder(w).Encode(response)
		log.Printf("Request completed successfully")
	})

	log.Printf("Starting server on port %d", cfg.ServerPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.ServerPort), nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
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
	currentItems := make(map[string]bool)
	var skippedCount, updatedCount int
	var mu sync.Mutex

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
			if hash, ok := payload["_hash"].GetKind().(*qdrant.Value_StringValue); ok && hash.StringValue == descHash {
				mu.Lock()
				skippedCount++
				mu.Unlock()
				continue
			}
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
