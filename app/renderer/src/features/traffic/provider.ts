// Registers the traffic source for the map and the topology (contract: features/map-webgl/trafficEdges.ts).
import { setTrafficProvider } from '../map-webgl/trafficEdges'
import { trafficApi } from './api'

setTrafficProvider((clusterId, namespace) => trafficApi.snapshot(clusterId, namespace))
