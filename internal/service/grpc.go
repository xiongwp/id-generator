package service

import (
	"context"

	"id-generator/internal/generator"
	pb "id-generator/internal/proto"
	"id-generator/internal/segment"
)

type Server struct {
	pb.UnimplementedIDServiceServer
	sf  *generator.Node
	seg *segment.Buffer
}

func (s *Server) GetID(ctx context.Context, _ *pb.IDRequest) (*pb.IDResponse, error) {
	id := s.seg.Next()
	if id == -1 {
		id = s.sf.NextID()
	}
	return &pb.IDResponse{Id: id}, nil
}
