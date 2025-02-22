package defaultadapter

import (
	"github.com/Kindling-project/kindling/collector/model"
	"github.com/Kindling-project/kindling/collector/model/constlabels"
	"github.com/Kindling-project/kindling/collector/model/constvalues"
	"go.opentelemetry.io/otel/attribute"
	"sort"
	"strconv"
	"sync"
)

// LabelConverter works label's transformation.It can reduce the memory allocation by using the sync.Pool.
type LabelConverter struct {
	labelsMap map[extraLabelsKey]realAttributes

	// updateKeys generals extraLabelsKey for incoming model.GaugeGroup
	updateKeys []updateKey

	// valueLabelsFunc generals labels from Gauge.Values
	valueLabelsFunc valueToLabels
	// adjustFunctions modify the final output
	adjustFunctions []adjustFunctions
}

type metricAdapterBuilder struct {
	baseAndCommonLabelsDict []dictionary

	extraLabelsParamList []extraLabelsParam
	extraLabelsKey       []extraLabelsKey
	updateKeys           []updateKey

	valueLabelsKey  []dictionary
	valueLabelsFunc valueToLabels

	constLabels     []attribute.KeyValue
	adjustFunctions []adjustFunctions
}

type realAttributes struct {
	// attrsListPool containers sorted []attribute.KeyValue
	attrsListPool *attrsListPool
	attrsMapPool  *attrsMapPool

	metricsDicList []dictionary
	// sortMap is a Map between the index of attrsListPool and metricsDicList
	sortMap map[int]int
}

type attrsListPool struct {
	templateAttrs []attribute.KeyValue
	attrsPool     *sync.Pool
}

type attrsMapPool struct {
	templateAttrs *model.AttributeMap
	attrsPool     *sync.Pool
}

type extraLabelsKey struct {
	protocol Protocol
}

type extraLabelsParam struct {
	dicList []dictionary
	extraLabelsKey
}

func updateProtocolKey(key *extraLabelsKey, labels *model.AttributeMap) *extraLabelsKey {
	switch labels.GetStringValue(constlabels.Protocol) {
	case constvalues.ProtocolHttp:
		key.protocol = HTTP
	case constvalues.ProtocolGrpc:
		key.protocol = GRPC
	case constvalues.ProtocolMysql:
		key.protocol = MYSQL
	case constvalues.ProtocolDns:
		key.protocol = DNS
	case constvalues.ProtocolKafka:
		key.protocol = KAFKA
	default:
		key.protocol = UNSUPPORTED
	}
	return key
}

type valueToLabels func(gaugeGroup *model.GaugeGroup) []attribute.KeyValue
type updateKey func(key *extraLabelsKey, labels *model.AttributeMap) *extraLabelsKey
type adjustAttrMaps func(labels *model.AttributeMap, attributeMap *model.AttributeMap) *model.AttributeMap
type adjustLabels func(labels *model.AttributeMap, attrs []attribute.KeyValue) []attribute.KeyValue

type adjustFunctions struct {
	adjustAttrMaps adjustAttrMaps
	adjustLabels   adjustLabels
}

func (key *extraLabelsKey) simpleMergeKey(labelsKey *extraLabelsKey) *extraLabelsKey {
	if key == nil {
		return labelsKey
	}
	if key.protocol == empty {
		key.protocol = labelsKey.protocol
	}
	return key
}

func (param *extraLabelsParam) simpleMergeParam(extraParams *extraLabelsParam) *extraLabelsParam {
	if param == nil {
		return extraParams
	} else {
		param.dicList = append(param.dicList, extraParams.dicList...)
		return param
	}
}

func newAdapterBuilder(
	baseDict []dictionary,
	commonLabels [][]dictionary) *metricAdapterBuilder {

	baseLabels := make([]attribute.KeyValue, len(baseDict))
	for j := 0; j < len(baseDict); j++ {
		baseLabels[j].Key = attribute.Key(baseDict[j].newKey)
	}
	if commonLabels != nil {
		for j := 0; j < len(commonLabels); j++ {
			for k := 0; k < len(commonLabels[j]); k++ {
				baseLabels = append(baseLabels, attribute.KeyValue{
					Key: attribute.Key(commonLabels[j][k].newKey),
				})
				baseDict = append(baseDict, commonLabels[j][k])
			}
		}
	}

	return &metricAdapterBuilder{
		baseAndCommonLabelsDict: baseDict,
		extraLabelsKey:          make([]extraLabelsKey, 0),
		adjustFunctions:         make([]adjustFunctions, 0),
	}
}

func (m *metricAdapterBuilder) withExtraLabels(params []extraLabelsParam, update updateKey) *metricAdapterBuilder {
	if m.extraLabelsKey == nil || len(m.extraLabelsKey) == 0 {
		m.extraLabelsKey = make([]extraLabelsKey, len(params))
		for i := 0; i < len(params); i++ {
			m.extraLabelsKey[i] = params[i].extraLabelsKey
		}
		m.extraLabelsParamList = params
		m.updateKeys = make([]updateKey, 1)
		m.updateKeys[0] = update
		return m
	}

	tmpNewExtraParamsList := make([]extraLabelsParam, len(m.extraLabelsParamList)*len(params))
	tmpNewExtraKeyList := make([]extraLabelsKey, len(m.extraLabelsKey)*len(params))

	for i := 0; i < len(params); i++ {
		for s := 0; s < len(m.extraLabelsKey); s++ {
			newKey := m.extraLabelsKey[s].simpleMergeKey(&params[i].extraLabelsKey)
			newParam := m.extraLabelsParamList[s].simpleMergeParam(&params[i])

			tmpNewExtraKeyList = append(tmpNewExtraKeyList, *newKey)
			tmpNewExtraParamsList = append(tmpNewExtraParamsList, *newParam)
		}
	}

	m.extraLabelsParamList = tmpNewExtraParamsList
	m.extraLabelsKey = tmpNewExtraKeyList

	return m
}

func (m *metricAdapterBuilder) withValueToLabels(keys []dictionary, valueToLabel valueToLabels) *metricAdapterBuilder {
	m.valueLabelsFunc = valueToLabel
	m.valueLabelsKey = keys
	return m
}

func (m *metricAdapterBuilder) withConstLabels(constLabels []attribute.KeyValue) *metricAdapterBuilder {
	m.constLabels = constLabels
	return m
}

func (m *metricAdapterBuilder) withAdjust(adjustFunc adjustFunctions) *metricAdapterBuilder {
	m.adjustFunctions = append(m.adjustFunctions, adjustFunc)
	return m
}

func (m *metricAdapterBuilder) build() (*LabelConverter, error) {
	labelsMap := make(map[extraLabelsKey]realAttributes, len(m.extraLabelsKey))
	baseAndCommonParams := make([]attribute.KeyValue, len(m.baseAndCommonLabelsDict))

	for i := 0; i < len(m.baseAndCommonLabelsDict); i++ {
		baseAndCommonParams[i] = attribute.KeyValue{
			Key: attribute.Key(m.baseAndCommonLabelsDict[i].newKey),
		}
	}

	for i := 0; i < len(m.extraLabelsKey); i++ {
		//TODO Check length of extraLabelsKey is equal to extraLabelsParamList , or return error
		tmpDict := make([]dictionary, 0, len(m.baseAndCommonLabelsDict)+len(m.extraLabelsParamList[i].dicList))
		tmpDict = append(tmpDict, m.baseAndCommonLabelsDict...)
		tmpDict = append(tmpDict, m.extraLabelsParamList[i].dicList...)
		tmpParamList := make([]attribute.KeyValue, len(baseAndCommonParams))
		copy(tmpParamList, baseAndCommonParams)
		for s := 0; s < len(m.extraLabelsParamList[i].dicList); s++ {
			tmpParamList = append(tmpParamList, attribute.KeyValue{
				Key: attribute.Key(m.extraLabelsParamList[i].dicList[s].newKey),
			})
		}

		// valueLabels
		if m.valueLabelsKey != nil {
			for s := 0; s < len(m.valueLabelsKey); s++ {
				tmpParamList = append(tmpParamList, attribute.KeyValue{
					Key: attribute.Key(m.valueLabelsKey[s].newKey),
				})
			}
		}

		if m.constLabels != nil {
			tmpParamList = append(tmpParamList, m.constLabels...)
		}

		// manual sort since otlp-sdk will sort our paramList
		tmpKeysList := make([]string, len(tmpParamList))
		for s := 0; s < len(tmpParamList); s++ {
			tmpKeysList[s] = string(tmpParamList[s].Key)
		}
		sort.Strings(tmpKeysList)
		sortCache := make(map[int]int, len(tmpParamList))
		realParamList := make([]attribute.KeyValue, len(tmpParamList))

		for s := 0; s < len(tmpKeysList); s++ {
			for j := 0; j < len(tmpParamList); j++ {
				if tmpKeysList[s] == string(tmpParamList[j].Key) {
					sortCache[j] = s
					realParamList[s] = tmpParamList[j]
					break
				}
			}
		}

		attrs := make(map[string]model.AttributeValue, len(realParamList))
		attrsMap := model.NewAttributeMapWithValues(attrs)
		for _, label := range m.constLabels {
			switch label.Value.Type() {
			case attribute.STRING:
				attrsMap.AddStringValue(string(label.Key), label.Value.AsString())
			case attribute.INT64:
				attrsMap.AddIntValue(string(label.Key), label.Value.AsInt64())
			case attribute.BOOL:
				attrsMap.AddBoolValue(string(label.Key), label.Value.AsBool())
			}
		}
		labelsMap[m.extraLabelsKey[i]] = realAttributes{
			attrsListPool:  createNewAttrsListPool(realParamList),
			attrsMapPool:   createNewAttrsMapPool(attrsMap),
			metricsDicList: tmpDict,
			sortMap:        sortCache,
		}
	}

	return &LabelConverter{
		labelsMap:       labelsMap,
		updateKeys:      m.updateKeys,
		valueLabelsFunc: m.valueLabelsFunc,
		adjustFunctions: m.adjustFunctions,
	}, nil
}

// transform is used to general final labels for Async Metric.It won't modify the origin model.GaugeGroup and should be free by calling the FreeAttrsMap after exported.
func (m *LabelConverter) transform(group *model.GaugeGroup) (*model.AttributeMap, FreeAttrsMap) {
	labels := group.Labels
	tmpExtraKey := &extraLabelsKey{protocol: empty}
	for i := 0; i < len(m.updateKeys); i++ {
		tmpExtraKey = m.updateKeys[i](tmpExtraKey, labels)
	}
	attrs := m.labelsMap[*tmpExtraKey]
	attrsMap := attrs.attrsMapPool.Get().(*model.AttributeMap)
	for i := 0; i < len(attrs.metricsDicList); i++ {
		switch attrs.metricsDicList[i].valueType {
		case String:
			attrsMap.AddStringValue(attrs.metricsDicList[i].newKey, labels.GetStringValue(attrs.metricsDicList[i].originKey))
		case Int64:
			attrsMap.AddIntValue(attrs.metricsDicList[i].newKey, labels.GetIntValue(attrs.metricsDicList[i].originKey))
		case Bool:
			attrsMap.AddBoolValue(attrs.metricsDicList[i].newKey, labels.GetBoolValue(attrs.metricsDicList[i].originKey))
		case FromInt64ToString:
			attrsMap.AddStringValue(attrs.metricsDicList[i].newKey, strconv.FormatInt(labels.GetIntValue(attrs.metricsDicList[i].originKey), 10))
		case StrEmpty:
			attrsMap.AddStringValue(attrs.metricsDicList[i].newKey, constlabels.STR_EMPTY)
		}
	}

	if m.valueLabelsFunc != nil {
		valueLabels := m.valueLabelsFunc(group)
		for i := 0; i < len(valueLabels); i++ {
			switch valueLabels[i].Value.Type() {
			case attribute.STRING:
				attrsMap.AddStringValue(string(valueLabels[i].Key), valueLabels[i].Value.AsString())
			case attribute.INT64:
				attrsMap.AddIntValue(string(valueLabels[i].Key), valueLabels[i].Value.AsInt64())
			case attribute.BOOL:
				attrsMap.AddBoolValue(string(valueLabels[i].Key), valueLabels[i].Value.AsBool())
			}
		}
	}

	for i := 0; i < len(m.adjustFunctions); i++ {
		attrsMap = m.adjustFunctions[i].adjustAttrMaps(labels, attrsMap)
	}
	return attrsMap, attrs.attrsMapPool.Free
}

// convert is used to general final labels for Sync Metric and Trace. It won't modify the origin model.GaugeGroup and should be free by calling the FreeAttrsList after exported.
func (m *LabelConverter) convert(group *model.GaugeGroup) ([]attribute.KeyValue, FreeAttrsList) {
	labels := group.Labels
	tmpExtraKey := &extraLabelsKey{protocol: empty}
	for i := 0; i < len(m.updateKeys); i++ {
		tmpExtraKey = m.updateKeys[i](tmpExtraKey, labels)
	}
	attrs := m.labelsMap[*tmpExtraKey]
	attrsList := attrs.attrsListPool.Get().([]attribute.KeyValue)
	for i := 0; i < len(attrs.metricsDicList); i++ {
		switch attrs.metricsDicList[i].valueType {
		case String:
			attrsList[attrs.sortMap[i]].Value = attribute.StringValue(labels.GetStringValue(attrs.metricsDicList[i].originKey))
		case Int64:
			attrsList[attrs.sortMap[i]].Value = attribute.Int64Value(labels.GetIntValue(attrs.metricsDicList[i].originKey))
		case Bool:
			attrsList[attrs.sortMap[i]].Value = attribute.BoolValue(labels.GetBoolValue(attrs.metricsDicList[i].originKey))
		case FromInt64ToString:
			attrsList[attrs.sortMap[i]].Value = attribute.StringValue(strconv.FormatInt(labels.GetIntValue(attrs.metricsDicList[i].originKey), 10))
		case StrEmpty:
			attrsList[attrs.sortMap[i]].Value = attribute.StringValue(constlabels.STR_EMPTY)
		}
	}

	if m.valueLabelsFunc != nil {
		valueLabels := m.valueLabelsFunc(group)
		for i := 0; i < len(valueLabels); i++ {
			attrsList[attrs.sortMap[i+len(attrs.metricsDicList)]].Value = valueLabels[i].Value
		}
	}

	for i := 0; i < len(m.adjustFunctions); i++ {
		attrsList = m.adjustFunctions[i].adjustLabels(labels, attrsList)
	}
	return attrsList, attrs.attrsListPool.Free
}

func (a *attrsListPool) createAttrsList() interface{} {
	attrsList := make([]attribute.KeyValue, len(a.templateAttrs))
	copy(attrsList, a.templateAttrs)
	return attrsList
}

func createNewAttrsListPool(attributes []attribute.KeyValue) *attrsListPool {
	a := &attrsListPool{
		templateAttrs: attributes,
	}
	a.attrsPool = &sync.Pool{New: a.createAttrsList}
	return a
}

func (a *attrsListPool) Get() interface{} {
	return a.attrsPool.Get()
}

func (a *attrsListPool) Free(attrsList []attribute.KeyValue) {
	a.attrsPool.Put(attrsList)
}

func createNewAttrsMapPool(attributes *model.AttributeMap) *attrsMapPool {
	a := &attrsMapPool{
		templateAttrs: attributes,
	}
	a.attrsPool = &sync.Pool{New: a.createAttrsMap}
	return a
}

func (a *attrsMapPool) createAttrsMap() interface{} {
	originValues := a.templateAttrs.GetValues()
	values := make(map[string]model.AttributeValue, len(originValues))
	for k, attr := range originValues {
		values[k] = attr
	}
	return model.NewAttributeMapWithValues(values)
}

func (a *attrsMapPool) Get() interface{} {
	return a.attrsPool.Get()
}

func (a *attrsMapPool) Free(attributeMap *model.AttributeMap) {
	a.attrsPool.Put(attributeMap)
}
