// Registers the traffic source for the map and the topology (contract: features/map-webgl/trafficEdges.ts).
// M5 traffic feature: replace the stub with the call to GET /api/clusters/{id}/traffic.
import { NO_TRAFFIC, setTrafficProvider } from '../map-webgl/trafficEdges'

setTrafficProvider(async () => NO_TRAFFIC)
