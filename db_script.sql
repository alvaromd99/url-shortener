-- Script to create the MySQL database for the project
drop database if exists BDurls;
create database BDurls;

use BDurls;

drop table if exists urls;
create table urls (
	id int auto_increment,
    original_url text not null,
    short_url varchar(5) not null unique,
    created_at datetime default current_timestamp,
    constraint primary key (id)
);

-- Utilizamos solo los 255 primeros caracteres para que sea mas eficiente
alter table urls add index idx_original_url (original_url(255));

-- MySQL pone los campos unique como indices automáticamente
-- show index from urls;