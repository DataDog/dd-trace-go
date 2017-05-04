BEGIN;

DROP TABLE IF EXISTS city;

CREATE TABLE city (
	id integer NOT NULL DEFAULT '0',
	name text,
	countrycode character(3),
	district text,
	population integer
);

INSERT INTO city VALUES
(1, 'Kabul', 'AFG', 'Kabol', 1780000),
(2, 'Qandahar', 'AFG', 'Qandahar', 237500),
(3, 'Herat', 'AFG', 'Herat', 186800),
(4, 'MazareSharif', 'AFG', 'Balkh', 127800),
(5, 'Amsterdam', 'NLD', 'NoordHolland', 731200),
(6, 'Rotterdam', 'NLD', 'ZuidHolland', 593321),
(7, 'Haag', 'NLD', 'ZuidHolland', 440900),
(8, 'Utrecht', 'NLD', 'Utrecht', 234323),
(9, 'Eindhoven', 'NLD', 'NoordBrabant', 201843),
(10, 'Tilburg', 'NLD', 'NoordBrabant', 193238),
(11, 'Groningen', 'NLD', 'Groningen', 172701),
(12, 'Breda', 'NLD', 'NoordBrabant', 160398),
(13, 'Apeldoorn', 'NLD', 'Gelderland', 153491),
(14, 'Nijmegen', 'NLD', 'Gelderland', 152463),
(15, 'Enschede', 'NLD', 'Overijssel', 149544),
(16, 'Haarlem', 'NLD', 'NoordHolland', 148772),
(17, 'Almere', 'NLD', 'Flevoland', 142465),
(18, 'Arnhem', 'NLD', 'Gelderland', 138020),
(19, 'Zaanstad', 'NLD', 'NoordHolland', 135621),
(20, 'Hertogenbosch', 'NLD', 'NoordBrabant', 129170);

COMMIT;
