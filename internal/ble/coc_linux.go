package ble

// Linux remains on the existing GATT image path until its native CoC connector
// is integrated; never claim transport support from the ESP capability alone.
func configureCoC(link *nativeLink, address string) {}
