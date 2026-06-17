package mta

// TripFeedURL returns the GTFS-RT protobuf feed URL for a subway route.
func TripFeedURL(routeID string) string {
	switch routeID {
	case "A", "C", "E", "H", "FS":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-ace"
	case "B", "D", "F", "M":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-bdfm"
	case "G":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-g"
	case "J", "Z":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-jz"
	case "L":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-l"
	case "N", "Q", "R", "W":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-nqrw"
	case "SI":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs-si"
	case "GS":
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs"
	default:
		// 1, 2, 3, 4, 5, 6, 7 and 42 St shuttle (GS uses numbered feed too)
		return "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs"
	}
}
