package music

import "github.com/makearr/Solarr/internal/media"

// node is a genre with its sub-genres, also known by Wikidata's "<genre> music"; the rest of MusicBrainz's genres find
// their place by the genre their name ends with ("alternative rock" under Rock).
func node(name string, children ...media.GenreTreeTag) media.GenreTreeTag {
	return media.GenreTreeTag{Name: name, Aliases: []string{name + " music"}, Children: children}
}

// GenreTree is the music genres MusicBrainz's and Wikidata's are mapped onto (plan 10-libraries L1).
var GenreTree = media.GenreTree{
	Key: "music", Label: "Music", Version: 3,
	Tags: []media.GenreTreeTag{
		node("Rock",
			node("Metal", media.GenreTreeTag{Name: "Heavy Metal"}),
			node("Punk", media.GenreTreeTag{Name: "Post-Punk"}),
			node("Grunge"), node("New Wave"), node("Rock and Roll", node("Rockabilly")), node("AOR"),
			node("Psychedelic", node("Neo-Psychedelia")), node("Glam"), node("Post-Hardcore"), node("Surf")),
		node("Pop", node("Synth-Pop"), node("Electropop"), node("Europop"), node("Schlager"), node("Ballad"), node("Britpop")),
		node("Electronic",
			node("House"), node("Techno"), node("Trance"), media.GenreTreeTag{Name: "Drum and Bass", Aliases: []string{"drum n bass", "dnb"}},
			node("Ambient"), node("Downtempo"), node("Trip Hop"), node("Electro"), node("Electronica"), node("Breakbeat"),
			node("Dubstep"), node("Industrial"), node("Big Beat"), node("Breaks"), node("Indietronica"), node("Happy Hardcore"),
			node("Rave"), node("UK Garage"), media.GenreTreeTag{Name: "EDM", Aliases: []string{"edm", "electronic dance music"}},
			node("Dance", node("Disco"), node("Eurodance"), node("Hi-NRG"), node("Club"))),
		{Name: "Hip Hop", Aliases: []string{"hip-hop", "rap", "hip hop music"}, Children: []media.GenreTreeTag{node("Trap"), node("Boom Bap")}},
		{Name: "R&B", Aliases: []string{"rnb", "rhythm and blues"}, Children: []media.GenreTreeTag{
			node("Soul"), node("Funk"), node("New Jack Swing"), node("Gospel"),
		}},
		node("Jazz", node("Swing"), node("Bebop"), node("Hard Bop"), node("Post-Bop"), media.GenreTreeTag{Name: "Fusion", Aliases: []string{"jazz fusion"}},
			node("Big Band")),
		node("Blues"),
		node("Country", node("Americana"), node("Bluegrass"), node("Country Pop"), node("Country Rock")),
		node("Folk", node("Singer-Songwriter"), node("Celtic")),
		node("Classical", node("Baroque"), node("Opera"), node("Romantic"), node("Contemporary Classical"), node("Classical Crossover")),
		node("Reggae", node("Dub"), node("Ska"), node("Dancehall")),
		node("Latin", node("Salsa"), node("Reggaeton"), node("Bossa Nova"), node("Latin Pop")),
		{Name: "Soundtrack", Aliases: []string{"film score", "score"}},
		node("World"), node("Experimental"), node("New Age"), node("Easy Listening"),
		{Name: "Christmas Music", Aliases: []string{"christmas"}},
		node("Comedy"), node("Spoken Word"), node("Children's Music"),
	},
}
