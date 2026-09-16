package lifecycle

import (
	"context"
	"time"

	"git.kuhree.com/kuhree.com/platform/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/platform/bin/carbonctl/internal/snapshots"
)

type FrameCapture interface {
	Capture(context.Context) ([]byte, error)
}

type SnapshotArchive interface {
	Save(context.Context, []byte, snapshots.Metadata) (snapshots.Metadata, error)
	Cleanup(context.Context, time.Duration) error
}

type RetainedSnapshots struct {
	Camera    FrameCapture
	Archive   SnapshotArchive
	Retention time.Duration
	Timeout   time.Duration
}

func (retained RetainedSnapshots) Capture(
	ctx context.Context, status printer.Status, event string,
) ([]byte, error) {
	timeout := retained.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	captureContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	frame, err := retained.Camera.Capture(captureContext)
	if err != nil {
		return nil, err
	}
	if _, err := retained.Archive.Save(ctx, frame, snapshots.Metadata{
		TakenAt: time.Now().UTC(), TaskID: status.TaskID, Event: event,
	}); err != nil {
		return nil, err
	}
	if retained.Retention > 0 {
		if err := retained.Archive.Cleanup(ctx, retained.Retention); err != nil {
			return nil, err
		}
	}
	return frame, nil
}
