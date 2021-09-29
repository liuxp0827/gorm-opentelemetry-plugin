package plugin

import (
	"context"
	"fmt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	"time"
)

const (
	gormSpanKey        = "gorm_tracer:span"
	callBackBeforeName = "gorm_tracer:before"
	callBackAfterName  = "gorm_tracer:after"
)

func (p *GormOpenTelemetryPlugin) startSpan(db *gorm.DB) (context.Context, trace.Span) {
	operation := fmt.Sprintf("%s", db.Dialector.Explain(db.Statement.SQL.String(), db.Statement.Vars...))
	return p.tracer.Start(db.Statement.Context,
		operation,
		trace.WithSpanKind(trace.SpanKindClient),
	)
}

func (p *GormOpenTelemetryPlugin) before(db *gorm.DB) {
	ctx, span := p.startSpan(db)
	// 利用db实例去传递span
	db.InstanceSet(gormSpanKey, span)
	db.Statement.WithContext(ctx)
	return
}

func (op *GormOpenTelemetryPlugin) after(db *gorm.DB) {
	var (
		tn    = time.Now()
		attrs = make([]attribute.KeyValue, 0)
	)
	// 从GORM的DB实例中取出span
	_span, exist := db.InstanceGet(gormSpanKey)
	if !exist {
		return
	}

	// 断言进行类型转换
	span, ok := _span.(trace.Span)
	if !ok {
		return
	}
	defer func() {
		span.End(trace.WithStackTrace(true), trace.WithTimestamp(tn))
	}()

	attrs = append(attrs, attribute.Key("args").String(fmt.Sprintf("%v", db.Statement.Vars)))
	attrs = append(attrs, attribute.Key("sql").String(db.Dialector.Explain(db.Statement.SQL.String(), db.Statement.Vars...)))
	attrs = append(attrs, attribute.Key("go.orm").String("gorm"))

	// Error
	if db.Error != nil {
		span.RecordError(db.Error, trace.WithTimestamp(tn))
	}
	return
}

type GormOpenTelemetryPlugin struct {
	tracer     trace.Tracer
	provider   trace.TracerProvider
	propagator propagation.TextMapPropagator
}

func NewGormOpenTelemetryPlugin(tp trace.TracerProvider) *GormOpenTelemetryPlugin {
	propagator := propagation.NewCompositeTextMapPropagator(Metadata{}, propagation.Baggage{}, propagation.TraceContext{})
	otel.SetTracerProvider(tp)
	tracer := otel.Tracer("db")
	return &GormOpenTelemetryPlugin{
		tracer:     tracer,
		provider:   tp,
		propagator: propagator,
	}
}

func (p *GormOpenTelemetryPlugin) Name() string {
	return "GORM-OpenTelemetry-Plugin"
}

func (p *GormOpenTelemetryPlugin) Initialize(db *gorm.DB) (err error) {
	// 开始前
	_ = db.Callback().Create().Before("gorm:before_create").Register(callBackBeforeName, p.before)
	_ = db.Callback().Query().Before("gorm:query").Register(callBackBeforeName, p.before)
	_ = db.Callback().Delete().Before("gorm:before_delete").Register(callBackBeforeName, p.before)
	_ = db.Callback().Update().Before("gorm:setup_reflect_value").Register(callBackBeforeName, p.before)
	_ = db.Callback().Row().Before("gorm:row").Register(callBackBeforeName, p.before)
	_ = db.Callback().Raw().Before("gorm:raw").Register(callBackBeforeName, p.before)

	// 结束后
	_ = db.Callback().Create().After("gorm:after_create").Register(callBackAfterName, p.after)
	_ = db.Callback().Query().After("gorm:after_query").Register(callBackAfterName, p.after)
	_ = db.Callback().Delete().After("gorm:after_delete").Register(callBackAfterName, p.after)
	_ = db.Callback().Update().After("gorm:after_update").Register(callBackAfterName, p.after)
	_ = db.Callback().Row().After("gorm:row").Register(callBackAfterName, p.after)
	_ = db.Callback().Raw().After("gorm:raw").Register(callBackAfterName, p.after)
	return
}
