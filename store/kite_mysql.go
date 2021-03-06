package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/golang/protobuf/proto"
	"github.com/sutoo/gorp"
	"kiteq/protocol"
	"log"
)

/**
CREATE DATABASE IF NOT EXISTS `kite` DEFAULT CHARACTER SET utf8 COLLATE utf8_general_ci ;
USE `kite` ;

CREATE TABLE IF NOT EXISTS `kite`.`kite_msg` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `messageId` CHAR(32) NOT NULL,
  `topic` VARCHAR(45) NULL DEFAULT 'default',
  `messageType` CHAR(4) NULL DEFAULT 0,
  `msgType` int(3) NOT NULL,
  `expiredTime` BIGINT NULL,
  `deliverCount` int(10) NOT NULL DEFAULT 0,
  `publishGroup` VARCHAR(45) NULL,
  `commit` TINYINT NULL DEFAULT 0 COMMENT '提交状态',
  `header` BLOB NOT NULL,
  `body` BLOB NOT NULL,
  `kiteq_server` char(32) NULL DEFAULT 0 COMMENT '在哪个拓扑里',
  INDEX `idx_commit` (`commit` ASC),
  INDEX `idx_msgId` (`messageId` ASC),
  PRIMARY KEY (`id`))
ENGINE = InnoDB;
*/
type KiteMysqlStore struct {
	addr  string
	dbmap *gorp.DbMap
}

func NewKiteMysql(addr string) *KiteMysqlStore {
	log.Println("[kite_mysql]NewKiteMysql", addr)
	db, err := sql.Open("mysql", addr)
	db.SetMaxIdleConns(100)
	db.SetMaxOpenConns(1024)
	if err != nil {
		log.Fatal("mysql can not connect")
	}

	// construct a gorp DbMap
	dbmap := &gorp.DbMap{Db: db, Dialect: gorp.MySQLDialect{"InnoDB", "UTF8"}}

	// add a table, setting the table name and
	// specifying that the Id property is an auto incrementing PK
	dbmap.AddTableWithName(MessageEntity{}, "kite_msg").SetKeys(false, "MessageId").SetHashKey("MessageId")
	dbmap.TypeConverter = CustomTypeConverter{}

	// create the table. in a production system you'd generally
	// use a migration tool, or create the tables via scripts
	err = dbmap.CreateTablesIfNotExists()
	if err != nil {
		log.Println("CreateTablesIfNotExists failed.")
	}
	ins := &KiteMysqlStore{
		addr:  addr,
		dbmap: dbmap,
	}
	return ins
}

//Converter for []string
type CustomTypeConverter struct {
}

func (me CustomTypeConverter) ToDb(val interface{}, fieldName string) (interface{}, error) {
	if val == nil {
		return nil, nil
	}
	//first bind by field name
	if fieldName == "Body" {
		s, stringOk := val.(string)
		if stringOk {
			return []byte(s), nil
		} else {
			b, byteOk := val.([]byte)
			if byteOk {
				return b, nil
			} else {
				return nil, errors.New("ToDb: unsuppoted type")
			}
		}
	}
	switch t := val.(type) {
	case []string:
		b, err := json.Marshal(t)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case *protocol.Header:
		data, err := proto.Marshal(t)
		if err != nil {
			fmt.Printf("ToDB proto.Marshal failed.%T, %v \n", val, val)
			return "", err
		}
		return data, nil
	}
	return val, nil
}

func (me CustomTypeConverter) FromDb(target interface{}, fieldName string) (gorp.CustomScanner, bool) {
	if fieldName == "Body" {
		binder := func(holder, target interface{}) error {
			s, stringOk := holder.(*string)
			if stringOk {
				t, ok := target.(*interface{})
				if ok {
					*t = (*s)
				}
			} else {
				b, byteOk := holder.(*[]byte)
				if byteOk {
					t, ok := target.(*interface{})
					if ok {
						*t = b
					}
				} else {
					log.Printf("FromDb: unsported type %T\n", holder)
					return errors.New("FromDb: unsported type.")
				}
			}
			return nil
		}
		return gorp.CustomScanner{new(string), target, binder}, true

	}

	switch target.(type) {
	case *[]string:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return errors.New("FromDb: Unable to convert to *string")
			}
			b := []byte(*s)
			return json.Unmarshal(b, target)
		}
		return gorp.CustomScanner{new(string), target, binder}, true
	case **protocol.Header:
		binder := func(holder, target interface{}) error {

			s, ok := holder.(*string)
			if !ok {
				return errors.New("FromDb: Unable to convert to string")
			}
			b := []byte(*s)
			tmp := &protocol.Header{}
			if err := proto.Unmarshal(b, tmp); err != nil {
				return err
			}
			targetP, ok := target.(**protocol.Header)
			*targetP = tmp

			return nil
		}
		return gorp.CustomScanner{new(string), target, binder}, true
	}
	return gorp.CustomScanner{}, false
}

func (self *KiteMysqlStore) Query(messageId string) *MessageEntity {
	obj, err := self.dbmap.Get(MessageEntity{}, messageId, messageId)
	if err != nil {
		log.Println(err)
		return nil
	}
	if obj == nil {
		return nil
	}
	entity := obj.(*MessageEntity)
	return entity

	// 是否需要额外设置头部的状态i?????
	// //设置一下头部的状态
	// entity.Header.Commit = proto.Bool(entity.Commit)
	// return entity
}

func (self *KiteMysqlStore) Save(entity *MessageEntity) bool {
	err := self.dbmap.Insert(entity)
	if err != nil {
		log.Println(err)
		return false
	}
	return true
}

func (self *KiteMysqlStore) Commit(messageId string) bool {
	values := make(map[string]interface{})
	values["Commit"] = true
	cond := make([]gorp.Cond, 1)
	cond[0] = gorp.Cond{
		Field:    "MessageId",
		Value:    messageId,
		Operator: "=",
	}
	updateCond := gorp.UpdateCond{
		Values: values,
		Cond:   cond,
		Ptr:    &MessageEntity{},
	}
	_, err := self.dbmap.UpdateByColumn(messageId, messageId, updateCond)
	if err != nil {
		log.Println(err)
		return false
	}
	return true
}

func (self *KiteMysqlStore) Delete(messageId string) bool {
	entity := &MessageEntity{MessageId: messageId}
	_, err := self.dbmap.Delete(entity)
	if err != nil {
		log.Println(err)
		return false
	}

	return true
}

func (self *KiteMysqlStore) Rollback(messageId string) bool {
	return self.Delete(messageId)
}

func (self *KiteMysqlStore) UpdateEntity(entity *MessageEntity) bool {
	_, err := self.dbmap.Update(entity)
	if err != nil {
		log.Println(err)
		return false
	}
	return true
}

func (self *KiteMysqlStore) PageQueryEntity(hashKey string, kiteServer string, nextDeliveryTime int64, startIdx, limit int32) (bool, []*MessageEntity) {
	cond := make([]*gorp.Cond, 2)
	cond[0] = &gorp.Cond{Field: "KiteServer", Operator: "=", Value: kiteServer}
	cond[1] = &gorp.Cond{Field: "NextDeliverTime", Operator: "<", Value: nextDeliveryTime}

	rawResults, err := self.dbmap.BatchGet(hashKey, startIdx, limit, MessageEntity{}, cond)
	if err != nil {
		log.Println(err)
		return false, nil
	}
	results := make([]*MessageEntity, len(rawResults))
	for _, v := range rawResults {
		results = append(results, v.(*MessageEntity))
	}
	return true, results
}
