module eris

go 1.18

replace github.com/eolso/athena => ../athena

replace github.com/eolso/apollo => ../apollo

//replace github.com/eolso/apollo/spotify => ../apollo/spotify

replace github.com/eolso/eris => ./

replace github.com/eolso/librespot-golang => ../librespot-golang

require (
	github.com/bwmarrin/discordgo v0.25.0
	github.com/eolso/apollo v0.0.0-00010101000000-000000000000
	github.com/eolso/athena v0.0.0-00010101000000-000000000000
	github.com/eolso/eris v0.0.0
	github.com/jonas747/dca v0.0.0-20210930103944-155f5e5f0cc7
	github.com/rs/zerolog v1.27.0
)

require (
	github.com/badfortrains/mdns v0.0.0-20160325001438-447166384f51 // indirect
	github.com/eolso/librespot-golang v0.0.0-00010101000000-000000000000 // indirect
	github.com/golang/protobuf v1.5.0 // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/jonas747/ogg v0.0.0-20161220051205-b4f6f4cf3757 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/miekg/dns v1.1.50 // indirect
	golang.org/x/crypto v0.0.0-20211215165025-cf75a172585e // indirect
	golang.org/x/mod v0.4.2 // indirect
	golang.org/x/net v0.0.0-20220624214902-1bab6f366d9e // indirect
	golang.org/x/sys v0.0.0-20220520151302-bc2c85ada10a // indirect
	golang.org/x/tools v0.1.7 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/protobuf v1.27.1 // indirect
)
