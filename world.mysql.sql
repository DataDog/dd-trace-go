SET AUTOCOMMIT=0;

DROP TABLE IF EXISTS `city`;

CREATE TABLE `city` (
	`id` INT(11) NOT NULL AUTO_INCREMENT,
	`mame` CHAR(35) NOT NULL DEFAULT '',
	`countrycode` CHAR(3) NOT NULL DEFAULT '',
	`district` CHAR(20) NOT NULL DEFAULT '',
	`population` INT(11) NOT NULL DEFAULT '0',
	PRIMARY KEY (`id`)
); 

INSERT INTO `city` VALUES (1,'Kabul','AFG','Kabol',1780000);
INSERT INTO `city` VALUES (2,'Qandahar','AFG','Qandahar',237500);
INSERT INTO `city` VALUES (3,'Herat','AFG','Herat',186800);
INSERT INTO `city` VALUES (4,'MazareSharif','AFG','Balkh',127800);
INSERT INTO `city` VALUES (5,'Amsterdam','NLD','NoordHolland',731200);
INSERT INTO `city` VALUES (6,'Rotterdam','NLD','ZuidHolland',593321);
INSERT INTO `city` VALUES (7,'Haag','NLD','ZuidHolland',440900);
INSERT INTO `city` VALUES (8,'Utrecht','NLD','Utrecht',234323);
INSERT INTO `city` VALUES (9,'Eindhoven','NLD','NoordBrabant',201843);
INSERT INTO `city` VALUES (10,'Tilburg','NLD','NoordBrabant',193238);
INSERT INTO `city` VALUES (11,'Groningen','NLD','Groningen',172701);
INSERT INTO `city` VALUES (12,'Breda','NLD','NoordBrabant',160398);
INSERT INTO `city` VALUES (13,'Apeldoorn','NLD','Gelderland',153491);
INSERT INTO `city` VALUES (14,'Nijmegen','NLD','Gelderland',152463);
INSERT INTO `city` VALUES (15,'Enschede','NLD','Overijssel',149544);
INSERT INTO `city` VALUES (16,'Haarlem','NLD','NoordHolland',148772);
INSERT INTO `city` VALUES (17,'Almere','NLD','Flevoland',142465);
INSERT INTO `city` VALUES (18,'Arnhem','NLD','Gelderland',138020);
INSERT INTO `city` VALUES (19,'Zaanstad','NLD','NoordHolland',135621);
INSERT INTO `city` VALUES (20,'Hertogenbosch','NLD','NoordBrabant',129170);

COMMIT;

SET AUTOCOMMIT=1;
