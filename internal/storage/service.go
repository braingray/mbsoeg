package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	qdrant "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"

	"mbsoeg/pkg/models"
)

// Service handles interactions with the Qdrant vector database
type Service struct {
	client       qdrant.CollectionsClient
	pointsClient qdrant.PointsClient
	collections  map[string]string
}

// NewService creates a new storage service
func NewService(host string, port int) (*Service, error) {
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", host, port), grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Qdrant: %v", err)
	}

	return &Service{
		client:       qdrant.NewCollectionsClient(conn),
		pointsClient: qdrant.NewPointsClient(conn),
		collections: map[string]string{
			"descriptions": "mbs_codes",
		},
	}, nil
}

// InitializeCollection creates the collections if they don't exist
func (s *Service) InitializeCollection(ctx context.Context) error {
	for _, collection := range s.collections {
		_, err := s.client.Create(ctx, &qdrant.CreateCollection{
			CollectionName: collection,
			VectorsConfig: &qdrant.VectorsConfig{
				Config: &qdrant.VectorsConfig_Params{
					Params: &qdrant.VectorParams{
						Size:     1536,
						Distance: qdrant.Distance_Cosine,
					},
				},
			},
		})
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to create collection %s: %v", collection, err)
		}
	}
	return nil
}

// GenerateHash creates a hash of the item's content to detect changes
func (s *Service) GenerateHash(item models.MBSItem) string {
	descriptionContent := fmt.Sprintf("%v-%v-%v-%v-%v-%v",
		item.Description,
		item.Benefit100,
		item.ScheduleFee,
		item.BenefitType,
		item.Category,
		item.ItemType,
	)
	descriptionHash := sha256.Sum256([]byte(descriptionContent))

	return hex.EncodeToString(descriptionHash[:])
}

// GetPoint retrieves a point from the specified collection
func (s *Service) GetPoint(ctx context.Context, itemNum string, collectionType string) (*qdrant.RetrievedPoint, error) {
	itemID, err := strconv.ParseUint(itemNum, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("error converting ItemNum %s to uint64: %v", itemNum, err)
	}

	collection, ok := s.collections[collectionType]
	if !ok {
		return nil, fmt.Errorf("invalid collection type: %s", collectionType)
	}

	resp, err := s.pointsClient.Get(ctx, &qdrant.GetPoints{
		CollectionName: collection,
		Ids: []*qdrant.PointId{
			{
				PointIdOptions: &qdrant.PointId_Num{
					Num: itemID,
				},
			},
		},
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{
				Enable: true,
			},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get point: %v", err)
	}

	if len(resp.Result) == 0 {
		return nil, nil
	}

	return resp.Result[0], nil
}

// UpsertPoint updates or inserts a point in the specified collection
func (s *Service) UpsertPoint(ctx context.Context, itemNum string, vector []float32, payload map[string]interface{}, collectionType string) error {
	itemID, err := strconv.ParseUint(itemNum, 10, 64)
	if err != nil {
		return fmt.Errorf("error converting ItemNum %s to uint64: %v", itemNum, err)
	}

	collection, ok := s.collections[collectionType]
	if !ok {
		return fmt.Errorf("invalid collection type: %s", collectionType)
	}

	// Convert payload map to Qdrant values
	qdrantPayload := make(map[string]*qdrant.Value)
	for key, value := range payload {
		switch v := value.(type) {
		case string:
			qdrantPayload[key] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: v}}
		case bool:
			qdrantPayload[key] = &qdrant.Value{Kind: &qdrant.Value_BoolValue{BoolValue: v}}
		case float64:
			qdrantPayload[key] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: v}}
		case int:
			qdrantPayload[key] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(v)}}
		case int64:
			qdrantPayload[key] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: v}}
		case nil:
			// Skip nil values
			continue
		default:
			// For other types, convert to string
			qdrantPayload[key] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: fmt.Sprintf("%v", v)}}
		}
	}

	_, err = s.pointsClient.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection,
		Points: []*qdrant.PointStruct{
			{
				Id: &qdrant.PointId{
					PointIdOptions: &qdrant.PointId_Num{
						Num: itemID,
					},
				},
				Vectors: &qdrant.Vectors{
					VectorsOptions: &qdrant.Vectors_Vector{
						Vector: &qdrant.Vector{
							Data: vector,
						},
					},
				},
				Payload: qdrantPayload,
			},
		},
	})

	return err
}

// DeletePoint removes a point from the specified collection
func (s *Service) DeletePoint(ctx context.Context, itemNum string, collectionType string) error {
	itemID, err := strconv.ParseUint(itemNum, 10, 64)
	if err != nil {
		return fmt.Errorf("error converting ItemNum %s to uint64: %v", itemNum, err)
	}

	collection, ok := s.collections[collectionType]
	if !ok {
		return fmt.Errorf("invalid collection type: %s", collectionType)
	}

	_, err = s.pointsClient.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Points{
				Points: &qdrant.PointsIdsList{
					Ids: []*qdrant.PointId{
						{
							PointIdOptions: &qdrant.PointId_Num{
								Num: itemID,
							},
						},
					},
				},
			},
		},
	})

	return err
}

// ScrollPoints retrieves all points from the specified collection
func (s *Service) ScrollPoints(ctx context.Context, collectionType string) ([]*qdrant.RetrievedPoint, error) {
	collection, ok := s.collections[collectionType]
	if !ok {
		return nil, fmt.Errorf("invalid collection type: %s", collectionType)
	}

	var allPoints []*qdrant.RetrievedPoint
	var offset *qdrant.PointId
	var limit uint32 = 100

	for {
		resp, err := s.pointsClient.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: collection,
			Limit:          &limit,
			Offset:         offset,
			WithPayload: &qdrant.WithPayloadSelector{
				SelectorOptions: &qdrant.WithPayloadSelector_Enable{
					Enable: true,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to scroll points: %v", err)
		}

		if len(resp.Result) == 0 {
			break
		}

		allPoints = append(allPoints, resp.Result...)
		if len(resp.Result) < int(limit) {
			break
		}

		offset = resp.NextPageOffset
	}

	return allPoints, nil
}
