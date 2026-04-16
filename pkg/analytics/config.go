package analytics

type Config struct {
	Enabled bool
	Segment SegmentConfig
}

type SegmentConfig struct {
	Key string
}
