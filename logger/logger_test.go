package logger

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
)

func TestNewAppLogger(t *testing.T) {
	convey.Convey("Get a logger", t, func() {
		log := NewAppLogger()
		convey.So(log, convey.ShouldNotBeNil)
	})
}

func TestWithLogger(t *testing.T) {
	convey.Convey("Add logger to context", t, func() {
		log := NewAppLogger()
		ctx := WithLogger(context.Background(), log)
		convey.So(ctx, convey.ShouldNotBeNil)
		convey.So(ctx.Value(loggerKey{}), convey.ShouldEqual, log)
	})
}

func TestFromContext(t *testing.T) {
	convey.Convey("Retrieve logger from context", t, func() {
		log := NewAppLogger()
		ctx := WithLogger(context.Background(), log)
		retrievedLog := FromContext(ctx)
		convey.So(retrievedLog, convey.ShouldEqual, log)
	})

	convey.Convey("Retrieve logger from empty context", t, func() {
		ctx := context.Background()
		retrievedLog := FromContext(ctx)
		convey.So(retrievedLog, convey.ShouldNotBeNil)
	})
}

func TestConfigureLogLevelLogger(t *testing.T) {
	convey.Convey("Configure logger with different log levels", t, func() {
		convey.Convey("Configure with info level", func() {
			config := ConfigureLogLevelLogger(InfoLevel)
			convey.So(config.Level.String(), convey.ShouldEqual, zap.InfoLevel.String())
		})

		convey.Convey("Configure with error level", func() {
			config := ConfigureLogLevelLogger(ErrorLevel)
			convey.So(config.Level.String(), convey.ShouldEqual, zap.ErrorLevel.String())
		})

		convey.Convey("Configure with debug level", func() {
			config := ConfigureLogLevelLogger(DebugLevel)
			convey.So(config.Level.String(), convey.ShouldEqual, zap.DebugLevel.String())
		})

		convey.Convey("Configure with default level", func() {
			config := ConfigureLogLevelLogger("")
			convey.So(config.Level.String(), convey.ShouldEqual, zap.InfoLevel.String())
		})
	})
}
